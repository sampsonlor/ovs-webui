package inventory

import (
	"cmp"
	"context"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/diagnostics"
)

// CheckLinkLACPTarget is the job admission check. It resolves the exact
// current object without collecting link data.
func (s *Service) CheckLinkLACPTarget(ctx context.Context, req diagnostics.LinkLACPRequest, claims authn.Claims) error {
	v, _, err := s.linkLACPRequestView(ctx, req, claims)
	if err != nil {
		return err
	}
	return s.linkLACPRecheck(ctx, v)
}

// ObserveLinkLACP reads one confirmed, fresh immutable OVSDB view. It does not
// fetch new data, list unrelated topology, or expose names or native options.
func (s *Service) ObserveLinkLACP(ctx context.Context, req diagnostics.LinkLACPRequest, claims authn.Claims) (diagnostics.LinkLACPSample, error) {
	var out diagnostics.LinkLACPSample
	v, row, err := s.linkLACPRequestView(ctx, req, claims)
	if err != nil {
		return out, err
	}
	out.Target, out.ObservedAt = req.Target, v.observation.Evidence.ObservedAt
	ports := []linkLACPRow{{row: row, id: req.Target.ID}}
	if req.Target.Kind == "bridge" {
		ports, out.Partial, err = linkLACPChildren(ctx, v, "Port", row.Values["ports"])
		if err != nil {
			return diagnostics.LinkLACPSample{}, err
		}
	}
	if len(ports) > req.PortLimit {
		ports, out.Truncated = ports[:req.PortLimit], true
	}
	remaining := diagnostics.MaxLinkLACPMembers
	configAllowed := slices.Contains(claims.Capabilities, "configuration.read")
	for _, port := range ports {
		members, incomplete, err := linkLACPChildren(ctx, v, "Interface", port.row.Values["interfaces"])
		if err != nil {
			return diagnostics.LinkLACPSample{}, err
		}
		out.Partial = out.Partial || incomplete || len(members) == 0
		if len(members) > remaining {
			members, out.Truncated = members[:remaining], true
		}
		remaining -= len(members)
		mode := "withheld"
		if configAllowed {
			mode = linkLACPMode(port.row)
		}
		item := diagnostics.LinkLACPPort{Ref: apitypes.Ref{Kind: "port", ID: port.id}, LACP: mode}
		for _, member := range members {
			link := textValue(member.row.Values["link_state"])
			if link != "up" && link != "down" {
				link = "unknown"
			}
			item.Members = append(item.Members, diagnostics.LinkLACPMember{
				Ref:  apitypes.Ref{Kind: "interface", ID: member.id},
				Link: link, LACPCurrent: linkLACPCurrent(member.row, mode),
			})
		}
		out.Ports = append(out.Ports, item)
	}
	// A disconnect, stale view or any replacement during extraction cannot
	// masquerade as a fresh diagnosis. All reads above use the same view.
	if err := s.linkLACPRecheck(ctx, v); err != nil {
		return diagnostics.LinkLACPSample{}, err
	}
	return out, nil
}

func (s *Service) linkLACPRequestView(ctx context.Context, req diagnostics.LinkLACPRequest, claims authn.Claims) (*view, Row, error) {
	if err := req.Authorize(claims); err != nil {
		return nil, Row{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Row{}, err
	}
	v, err := s.linkLACPView()
	if err != nil {
		return nil, Row{}, err
	}
	table := "Port"
	if req.Target.Kind == "bridge" {
		table = "Bridge"
	}
	row, err := linkLACPTarget(ctx, v, table, req.Target.ID)
	return v, row, err
}

func (s *Service) linkLACPRecheck(ctx context.Context, v *view) error {
	current, err := s.linkLACPView()
	if err != nil {
		return err
	}
	if current != v {
		return apitypes.Fail(503, "DIAGNOSTIC_EVIDENCE_UNAVAILABLE")
	}
	return ctx.Err()
}

func (s *Service) linkLACPView() (*view, error) {
	if !s.mu.TryRLock() {
		return nil, apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	defer s.mu.RUnlock()
	v := s.current
	if v == nil || s.failure != "" || v.decision.State != "confirmed" || v.observation.Evidence.ObservedAt.IsZero() {
		return nil, apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	age := s.now().Sub(v.observation.Evidence.ObservedAt)
	if age < 0 || age > FreshFor {
		return nil, apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	return v, nil
}

func linkLACPTarget(ctx context.Context, v *view, table, id string) (Row, error) {
	var target Binding
	for _, b := range v.decision.Bindings {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if b.ManagementID == id && b.State == "active" {
			if target.ManagementID != "" {
				return Row{}, apitypes.Fail(503, "DIAGNOSTIC_EVIDENCE_UNAVAILABLE")
			}
			target = b
		}
	}
	if target.ManagementID == "" || target.Table != table {
		return Row{}, apitypes.Fail(404, "NOT_FOUND")
	}
	row := v.observation.Rows[table][target.UUID]
	if row.UUID == "" || row.UUID != target.UUID || v.decision.Bindings[Key(table, target.UUID)] != target {
		return Row{}, apitypes.Fail(503, "DIAGNOSTIC_EVIDENCE_UNAVAILABLE")
	}
	return row, nil
}

type linkLACPRow struct {
	row Row
	id  string
}

func linkLACPChildren(ctx context.Context, v *view, table string, raw any) ([]linkLACPRow, bool, error) {
	var partial bool
	switch value := raw.(type) {
	case string:
	case []any:
		if len(value) > MaxRows {
			return nil, false, apitypes.Fail(503, "DIAGNOSTIC_EVIDENCE_UNAVAILABLE")
		}
		for _, member := range value {
			if _, ok := member.(string); !ok {
				partial = true
				break
			}
		}
	default:
		return nil, true, nil
	}
	all := refs(raw)
	children := make([]linkLACPRow, 0, len(all))
	seen := map[string]bool{}
	for _, uuid := range all {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		binding := v.decision.Bindings[Key(table, uuid)]
		row := v.observation.Rows[table][uuid]
		if uuid == "" || binding.Table != table || binding.State != "active" || binding.UUID != uuid ||
			row.UUID != uuid || !apitypes.ManagementID(binding.ManagementID) || seen[binding.ManagementID] {
			partial = true
			continue
		}
		seen[binding.ManagementID] = true
		children = append(children, linkLACPRow{row: row, id: binding.ManagementID})
	}
	slices.SortFunc(children, func(a, b linkLACPRow) int { return cmp.Compare(a.id, b.id) })
	return children, partial, nil
}

func linkLACPMode(port Row) string {
	mode, known := optionalString(port, "lacp")
	if !known {
		return "unknown"
	}
	if mode == nil {
		return "off"
	}
	switch *mode {
	case "active", "passive", "off":
		return *mode
	default:
		return "unknown"
	}
}

func linkLACPCurrent(member Row, mode string) string {
	value := member.Values["lacp_current"]
	if current, ok := one(value).(bool); ok {
		if current {
			return "current"
		}
		return "not-current"
	}
	if set, ok := value.([]any); ok && len(set) == 0 && mode == "off" {
		return "not-applicable"
	}
	return "unknown"
}
