package inventory

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
)

func linkLACPFixture(t *testing.T) (*Service, apitypes.Ref, apitypes.Ref, apitypes.Ref, authn.Claims) {
	t.Helper()
	observed := time.Now()
	s := New(nil)
	s.now = func() time.Time { return observed }
	s.failure = ""
	v := &view{observation: Observation{Rows: Rows{}, Evidence: Evidence{ObservedAt: observed}},
		decision: Decision{State: "confirmed", Bindings: map[string]Binding{}}}
	s.current = v
	object := func(table string, n int, values map[string]any) (string, string) {
		native := fmt.Sprintf("ea65a4d0-e6ac-4e94-8ff2-%012d", n)
		id := fmt.Sprintf("76f12158-f9ce-4585-bf31-%012d", n)
		if v.observation.Rows[table] == nil {
			v.observation.Rows[table] = map[string]Row{}
		}
		v.observation.Rows[table][native] = Row{UUID: native, Values: values}
		v.decision.Bindings[Key(table, native)] = Binding{ManagementID: id, Table: table, UUID: native, State: "active"}
		return native, id
	}
	first, iid := object("Interface", 1, map[string]any{"link_state": []any{"up"}, "lacp_current": []any{true}, "name": "private-label"})
	second, _ := object("Interface", 2, map[string]any{"link_state": []any{"down"}, "lacp_current": []any{}})
	other, _ := object("Interface", 3, map[string]any{"link_state": []any{"up"}, "lacp_current": []any{false}})
	a, pid := object("Port", 4, map[string]any{"interfaces": []any{first}, "lacp": []any{"active"}})
	b, _ := object("Port", 5, map[string]any{"interfaces": []any{second}, "lacp": []any{}})
	c, _ := object("Port", 6, map[string]any{"interfaces": []any{other}, "lacp": []any{"passive"}})
	_, bid := object("Bridge", 7, map[string]any{"ports": []any{b, a}})
	_, _ = object("Bridge", 8, map[string]any{"ports": []any{c}})
	claims := authn.Claims{Capabilities: []string{"diagnostics.read", "inventory.read", "configuration.read"}}
	return s, apitypes.Ref{Kind: "bridge", ID: bid}, apitypes.Ref{Kind: "port", ID: pid},
		apitypes.Ref{Kind: "interface", ID: iid}, claims
}

func TestLinkLACPObservationUsesOnlyExactScopedBindings(t *testing.T) {
	s, bridge, port, iface, claims := linkLACPFixture(t)
	req := diagnostics.LinkLACPRequest{Target: bridge, SampleSeconds: 5, PortLimit: 2}
	if err := s.CheckLinkLACPTarget(t.Context(), req, claims); err != nil {
		t.Fatal(err)
	}
	result, err := diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "complete" || len(result.Ports) != 2 || result.Ports[0].Ref != port ||
		result.Ports[0].LACP != "active" || len(result.Ports[0].Members) != 1 ||
		result.Ports[0].Members[0] != (diagnostics.LinkLACPMember{Ref: iface, Link: "up", LACPCurrent: "current"}) ||
		result.Ports[1].LACP != "off" || result.Ports[1].Members[0].LACPCurrent != "not-applicable" {
		t.Fatalf("bad scoped sample: %+v, %v", result, err)
	}
	req.PortLimit = 1
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "truncated" || len(result.Ports) != 1 || result.Ports[0].Ref != port {
		t.Fatalf("expected deterministic bounded bridge sample: %+v, %v", result, err)
	}
	req.Target = apitypes.Ref{Kind: "port", ID: bridge.ID}
	if err := s.CheckLinkLACPTarget(t.Context(), req, claims); err == nil {
		t.Fatal("admission accepted mismatched kind")
	}
	_, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	var problem *apitypes.Problem
	if !errors.As(err, &problem) || problem.Status != 404 {
		t.Fatalf("wrong target kind resolved a live row: %v", err)
	}
	req.Target = port
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "complete" || len(result.Ports) != 1 || result.Ports[0].Ref != port {
		t.Fatalf("port request escaped the exact scope: %+v, %v", result, err)
	}
	claims.Capabilities = []string{"diagnostics.read", "inventory.read"}
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "partial" || result.Ports[0].LACP != "withheld" ||
		result.Ports[0].Members[0].LACPCurrent != "current" {
		t.Fatalf("configuration permission did not fence existing runtime read: %+v, %v", result, err)
	}
}

func TestLinkLACPObservationDistinguishesMissingDataAndPressure(t *testing.T) {
	s, bridge, port, iface, claims := linkLACPFixture(t)
	req := diagnostics.LinkLACPRequest{Target: port, SampleSeconds: 5, PortLimit: 1}
	for key, b := range s.current.decision.Bindings {
		if b.ManagementID == iface.ID {
			b.State = "retired"
			s.current.decision.Bindings[key] = b
		}
	}
	result, err := diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "partial" || len(result.Ports) != 1 || len(result.Ports[0].Members) != 0 {
		t.Fatalf("retired binding returned a member: %+v, %v", result, err)
	}
	for _, b := range s.current.decision.Bindings {
		if b.ManagementID == bridge.ID {
			s.current.observation.Rows["Bridge"][b.UUID].Values["ports"] = []any{}
		}
	}
	req.Target = bridge
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "no-data" || len(result.Ports) != 0 {
		t.Fatalf("empty fresh bridge was not classified as no-data: %+v, %v", result, err)
	}
	s.mu.Lock()
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	s.mu.Unlock()
	if err != nil || result.State != "provider-unavailable" || result.ObservedAt != nil {
		t.Fatalf("busy cache returned evidence: %+v, %v", result, err)
	}
	s.now = func() time.Time { return s.current.observation.Evidence.ObservedAt.Add(FreshFor + time.Nanosecond) }
	result, err = diagnostics.RunLinkLACP(t.Context(), s, req, claims)
	if err != nil || result.State != "provider-unavailable" || result.ObservedAt != nil {
		t.Fatalf("stale cache returned current evidence: %+v, %v", result, err)
	}
}
