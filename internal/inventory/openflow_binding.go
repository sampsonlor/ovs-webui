package inventory

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
)

// OpenFlowBridgeSource is private provider input for a caller-authorized read.
// DatapathID is the daemon-reported Bridge.datapath_id from this view.
type OpenFlowBridgeSource struct {
	Bridge     Binding
	Generation string
	Name       string
	DatapathID uint64
	Evidence   Evidence
}

// OpenFlowSource never resolves a configured datapath ID or a name supplied
// by a browser. The provider must independently prove the installed Bridge.
func (s *Service) OpenFlowSource(ctx context.Context, id string) (OpenFlowBridgeSource, error) {
	var out OpenFlowBridgeSource
	if !apitypes.ManagementID(id) {
		return out, apitypes.Fail(422, "INVALID_OPENFLOW_BRIDGE")
	}
	if s == nil || !s.mu.TryRLock() {
		return out, apitypes.Fail(503, "OPENFLOW_INVENTORY_UNAVAILABLE")
	}
	defer s.mu.RUnlock()
	v := s.current
	if !statisticViewFresh(v, s.failure, s.now()) || !apitypes.ManagementID(v.decision.Generation) {
		return out, apitypes.Fail(503, "OPENFLOW_INVENTORY_UNAVAILABLE")
	}
	proof := v.observation.Evidence
	roots := v.observation.Rows["Open_vSwitch"]
	root := roots[proof.Root]
	if len(roots) != 1 || !apitypes.UUID(proof.Root) || root.UUID != proof.Root {
		return out, apitypes.Fail(503, "OPENFLOW_BRIDGE_UNPROVEN")
	}
	var match Binding
	for _, b := range v.decision.Bindings {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if b.ManagementID != id || b.State != "active" {
			continue
		}
		if match.ManagementID != "" {
			return out, apitypes.Fail(503, "OPENFLOW_BRIDGE_UNPROVEN")
		}
		match = b
	}
	if match.Table != "Bridge" {
		return out, apitypes.Fail(404, "NOT_FOUND")
	}
	row := v.observation.Rows["Bridge"][match.UUID]
	name, known := row.Values["name"].(string)
	if !apitypes.UUID(match.UUID) || row.UUID != match.UUID || !known || !spanningtree.RuntimeName(name) ||
		v.decision.Bindings[Key("Bridge", match.UUID)] != match || !slices.Contains(refs(root.Values["bridges"]), match.UUID) {
		return out, apitypes.Fail(503, "OPENFLOW_BRIDGE_UNPROVEN")
	}
	text := textValue(row.Values["datapath_id"])
	if len(text) != 16 || strings.Trim(text, "0123456789abcdef") != "" {
		return out, apitypes.Fail(503, "OPENFLOW_DPID_UNOBSERVED")
	}
	dpid, err := strconv.ParseUint(text, 16, 64)
	if err != nil || dpid == 0 {
		return out, apitypes.Fail(503, "OPENFLOW_DPID_UNOBSERVED")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return OpenFlowBridgeSource{Bridge: match, Generation: v.decision.Generation, Name: name, DatapathID: dpid, Evidence: proof}, nil
}
