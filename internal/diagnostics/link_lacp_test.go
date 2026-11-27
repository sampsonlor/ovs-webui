package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

var bridge = apitypes.Ref{Kind: "bridge", ID: "3e2a672c-4f42-4f23-ae92-000000000001"}
var port = apitypes.Ref{Kind: "port", ID: "3e2a672c-4f42-4f23-ae92-000000000002"}
var member = apitypes.Ref{Kind: "interface", ID: "3e2a672c-4f42-4f23-ae92-000000000003"}
var allowed = authn.Claims{Capabilities: []string{LinkLACPCapability, "inventory.read", "configuration.read"}}

type linkSourceFunc func(context.Context, LinkLACPRequest, authn.Claims) (LinkLACPSample, error)

func (f linkSourceFunc) ObserveLinkLACP(ctx context.Context, r LinkLACPRequest, c authn.Claims) (LinkLACPSample, error) {
	return f(ctx, r, c)
}

func TestLinkLACPFencesAdmissionBeforeProvider(t *testing.T) {
	source := linkSourceFunc(func(context.Context, LinkLACPRequest, authn.Claims) (LinkLACPSample, error) {
		t.Fatal("invalid request reached provider")
		return LinkLACPSample{}, nil
	})
	for _, tt := range []struct {
		name   string
		req    LinkLACPRequest
		claims authn.Claims
		code   string
	}{
		{"inventory grant", LinkLACPRequest{port, 5, 1}, authn.Claims{Capabilities: []string{LinkLACPCapability}}, "CAPABILITY_DENIED"},
		{"diagnostic grant", LinkLACPRequest{port, 5, 1}, authn.Claims{Capabilities: []string{"inventory.read"}}, "CAPABILITY_DENIED"},
		{"display name", LinkLACPRequest{apitypes.Ref{Kind: "port", ID: "bond-storage"}, 5, 1}, allowed, "INVALID_DIAGNOSTIC_TARGET"},
		{"wrong kind", LinkLACPRequest{member, 5, 1}, allowed, "INVALID_DIAGNOSTIC_TARGET"},
		{"sample", LinkLACPRequest{port, 20, 1}, allowed, "INVALID_DIAGNOSTIC_SAMPLE_BUDGET"},
		{"ports", LinkLACPRequest{bridge, 15, 11}, allowed, "INVALID_DIAGNOSTIC_PORT_LIMIT"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RunLinkLACP(t.Context(), source, tt.req, tt.claims)
			var problem *apitypes.Problem
			if !errors.As(err, &problem) || problem.Code != tt.code {
				t.Fatalf("want %s, got %v", tt.code, err)
			}
		})
	}
}

func TestLinkLACPSanitizesAndFencesProviderOutput(t *testing.T) {
	const secret = "ovsg_synthetic-secret"
	sample := LinkLACPSample{Target: port, ObservedAt: time.Now(), Ports: []LinkLACPPort{{
		Ref: port, LACP: secret, Members: []LinkLACPMember{{Ref: member, Link: secret, LACPCurrent: secret}},
	}}}
	source := linkSourceFunc(func(context.Context, LinkLACPRequest, authn.Claims) (LinkLACPSample, error) {
		return sample, nil
	})
	req := LinkLACPRequest{port, 5, 1}
	out, err := RunLinkLACP(t.Context(), source, req, allowed)
	body, marshalErr := json.Marshal(out)
	if err != nil || marshalErr != nil || out.State != "partial" || len(out.Ports) != 1 ||
		out.Ports[0].Members[0].LACPCurrent != "unknown" || strings.Contains(string(body), secret) {
		t.Fatalf("unbounded or unsanitized provider output: %s, %v", body, err)
	}

	sample.Ports[0].Ref.ID = "3e2a672c-4f42-4f23-ae92-000000000004"
	out, err = RunLinkLACP(t.Context(), source, req, allowed)
	if err != nil || out.State != "evidence-unavailable" || out.ObservedAt != nil || len(out.Ports) != 0 {
		t.Fatalf("mismatched object returned evidence: %+v, %v", out, err)
	}

	sample.Ports[0].Ref = port
	sample.Ports[0].Members = make([]LinkLACPMember, MaxLinkLACPMembers+1)
	out, err = RunLinkLACP(t.Context(), source, req, allowed)
	if err != nil || out.State != "evidence-unavailable" || len(out.Ports) != 0 {
		t.Fatalf("provider exceeded member limit: %+v, %v", out, err)
	}
}

func TestLinkLACPCancelTimeoutAndUnavailable(t *testing.T) {
	source := linkSourceFunc(func(ctx context.Context, _ LinkLACPRequest, _ authn.Claims) (LinkLACPSample, error) {
		<-ctx.Done()
		return LinkLACPSample{}, ctx.Err()
	})
	req := LinkLACPRequest{port, 5, 1}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := RunLinkLACP(ctx, source, req, allowed)
	if err != nil || out.State != "cancelled" {
		t.Fatalf("want cancellation, got %+v, %v", out, err)
	}
	ctx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	out, err = RunLinkLACP(ctx, source, req, allowed)
	if err != nil || out.State != "timed-out" {
		t.Fatalf("want timeout, got %+v, %v", out, err)
	}
	out, err = RunLinkLACP(t.Context(), nil, req, allowed)
	if err != nil || out.State != "provider-unavailable" {
		t.Fatalf("want provider unavailable, got %+v, %v", out, err)
	}
	failed := linkSourceFunc(func(context.Context, LinkLACPRequest, authn.Claims) (LinkLACPSample, error) {
		return LinkLACPSample{}, errors.New("ovsg_synthetic-private-provider-error")
	})
	out, err = RunLinkLACP(t.Context(), failed, req, allowed)
	if err != nil || out.State != "operation-failed" || out.Reason != "DIAGNOSTIC_OPERATION_FAILED" {
		t.Fatalf("provider error was not sanitized: %+v, %v", out, err)
	}
}
