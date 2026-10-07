package inventory

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// Root policy is an explicit declaration of exclusive Linux ingress management,
// not an inference from an observed police action or an MTU/Port grant.
func (s *Service) SetLocalPolicingInterfaces(ids []string) error {
	if len(ids) > 128 {
		return apitypes.Fail(422, "INVALID_POLICING_AUTHORITY")
	}
	allow := map[string]bool{}
	for _, id := range ids {
		if !apitypes.ManagementID(id) || allow[id] {
			return apitypes.Fail(422, "INVALID_POLICING_AUTHORITY")
		}
		allow[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localPolicing = allow
	return nil
}
func nativePolicing(row Row) (candidate.PolicingConfig, bool) {
	var p candidate.PolicingConfig
	for n, field := range []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"} {
		text, ok := row.Values[field].(string)
		v, err := strconv.ParseInt(text, 10, 32)
		if !ok || err != nil || v < 0 || strconv.FormatInt(v, 10) != text {
			return p, false
		}
		*[]*int{&p.Rate, &p.Burst, &p.PacketRate, &p.PacketBurst}[n] = int(v)
	}
	return p, candidate.ValidPolicing(p)
}
func projectPolicing(v *view, allow map[string]bool, out *candidate.Snapshot, bindings []candidate.Binding) {
	out.Policings = map[string]candidate.InterfacePolicing{}
	compatible := 0
	for _, t := range v.observation.Schema.Tables {
		if t.Name == "Interface" {
			for _, c := range t.Columns {
				if c.Monitored && c.PolicingCompatible {
					compatible++
				}
			}
		}
	}
	for _, requested := range bindings {
		if requested.Table != "Interface" {
			continue
		}
		b := v.decision.Bindings[Key("Interface", requested.OVSUUID)]
		if b.State != "active" || b.ManagementID != requested.ManagementID {
			continue
		}
		row := v.observation.Rows["Interface"][b.UUID]
		p := candidate.InterfacePolicing{Binding: candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Interface", Generation: out.Generation}, Supported: compatible == 4, Authority: "unknown"}
		p.Configuration, p.Known = nativePolicing(row)
		req, deviceKnown := deviceRequest(row)
		p.Name, p.IfIndex = req.Name, req.IfIndex
		p.Type, _ = row.Values["type"].(string)
		port, portKnown := parent(v, "Port", "interfaces", b.UUID)
		bridge, bridgeKnown := parent(v, "Bridge", "ports", port.UUID)
		bind := func(table string, r Row) candidate.Binding {
			b := v.decision.Bindings[Key(table, r.UUID)]
			if b.State != "active" {
				return candidate.Binding{}
			}
			return candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: table, Generation: out.Generation}
		}
		p.Port, p.Bridge = bind("Port", port), bind("Bridge", bridge)
		root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
		other, otherKnown := root.Values["other_config"].(map[string]any)
		offloadDisabled := otherKnown && (other["hw-offload"] == nil || other["hw-offload"] == "false")
		if allow[b.ManagementID] {
			p.Authority = "local-exclusive"
		}
		for _, r := range []Row{root, bridge, port, row} {
			if externalControl(r.Values["external_ids"]) {
				p.Authority = "externally-controlled"
			}
		}
		errors, errorKnown := row.Values["error"].([]any)
		ofportKnown := false
		if values, ok := row.Values["ofport"].([]any); ok && len(values) == 1 {
			if text, ok := values[0].(string); ok {
				n, err := strconv.Atoi(text)
				ofportKnown = err == nil && n > 0 && n <= 65534
			}
		}
		p.Eligible = portKnown && bridgeKnown && p.Port.ManagementID != "" && p.Bridge.ManagementID != "" && len(v.observation.Rows["Open_vSwitch"]) == 1 && slices.Contains(refs(root.Values["bridges"]), bridge.UUID) && bridge.Values["datapath_type"] == "system" && p.Type == "internal" && deviceKnown && len(p.Name) <= 15 && row.Values["name"] == port.Values["name"] && port.Values["name"] != bridge.Values["name"] && len(refs(port.Values["interfaces"])) == 1 && row.InterfaceOptionsEmpty != nil && *row.InterfaceOptionsEmpty && offloadDisabled && errorKnown && len(errors) == 0 && ofportKnown
		p.Dependency = Digest([]any{root.UUID, other, p.Port, p.Bridge, bridge.Values["name"], bridge.Values["datapath_type"], bridge.Values["ports"], port.Values["name"], port.Values["interfaces"], p.Name, p.Type, p.IfIndex, row.Values["options"], row.InterfaceOptionsEmpty, p.Authority})
		out.Policings[b.ManagementID] = p
	}
	out.Policy = Digest([]any{out.Policy, allow})
	out.Revision = Digest([]any{out.Revision, out.Policings})
}

// Execution uses a separate, private qualification of the kernel read. The
// public diagnostic alone never grants writes, ownership or Applied evidence.
func (s *Service) PolicingEvidence(ctx context.Context, expected candidate.InterfacePolicing) (PolicingSample, error) {
	bindings := []candidate.Binding{expected.Binding}
	before, err := s.CandidateSnapshot(ctx, bindings)
	if err != nil {
		return PolicingSample{}, err
	}
	p := before.Policings[expected.Binding.ManagementID]
	if p != expected || !candidate.PolicingEditable(p) {
		return PolicingSample{}, apitypes.Fail(409, "POLICING_BINDING_CHANGED")
	}
	s.mu.RLock()
	observer := s.policingObserver
	s.mu.RUnlock()
	if observer == nil {
		return PolicingSample{}, apitypes.Fail(503, "POLICING_KERNEL_UNAVAILABLE")
	}
	sample := observer.ObservePolicing(ctx, DeviceRequest{Name: p.Name, IfIndex: p.IfIndex})
	after, err := s.CandidateSnapshot(ctx, bindings)
	if err != nil {
		return PolicingSample{}, err
	}
	age := s.now().Sub(sample.ObservedAt)
	if ctx.Err() != nil || after.Policings[p.Binding.ManagementID] != p || (sample.Availability != "known" && sample.Availability != "partial") || sample.IfIndex != p.IfIndex || age < 0 || age > time.Second || !validPolicingSample(sample) || len(sample.ConfigurationDigest) != 64 {
		return PolicingSample{}, apitypes.Fail(409, "POLICING_KERNEL_UNPROVEN")
	}
	if sample.Availability == "partial" || !sample.WriteCompatible {
		return PolicingSample{}, apitypes.Fail(409, "POLICING_KERNEL_CONFLICT")
	}
	return sample, nil
}
func PolicingMatches(sample PolicingSample, p candidate.PolicingConfig) bool {
	if !candidate.ValidPolicing(p) || sample.Availability != "known" || !sample.WriteCompatible {
		return false
	}
	if p.Rate == 0 && p.PacketRate == 0 {
		return len(sample.Actions) == 0 && sample.IngressKind == "none"
	}
	if sample.IngressKind != "ingress" || len(sample.Actions) != 1 {
		return false
	}
	a := sample.Actions[0]
	if a.FilterKind != "matchall" || a.Priority != 1 || a.Handle != "0x00000001" || a.ExceedAction != "drop" || a.ConformAction != "continue" {
		return false
	}
	if p.Rate != 0 {
		return a.BytesPerSecond != nil && *a.BytesPerSecond == strconv.Itoa(p.Rate*125) && a.PacketsPerSecond == nil
	}
	return a.PacketsPerSecond != nil && *a.PacketsPerSecond == strconv.Itoa(p.PacketRate*1000) && a.BytesPerSecond == nil
}
