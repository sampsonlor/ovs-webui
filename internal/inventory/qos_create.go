package inventory

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func (s *Service) SetLocalQoSCreateNames(names []string) error {
	if len(names) > candidate.QoSCreateMaxNames {
		return apitypes.Fail(422, "INVALID_QOS_AUTHORITY")
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		if !candidate.ValidQoSProfileName(name) || allowed[name] {
			return apitypes.Fail(422, "INVALID_QOS_AUTHORITY")
		}
		allowed[name] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localQoSCreateNames = allowed
	return nil
}

func qosProfileAlias(row Row) string {
	labels, _ := row.Configuration["external_ids"].(map[string]any)
	name := textValue(labels[candidate.QoSProfileName])
	if candidate.ValidQoSProfileName(name) {
		return name
	}
	return ""
}

func qosCreateState(v *view, names map[string]bool, bindings []candidate.Binding) candidate.QoSCreateState {
	rootID := v.observation.Evidence.Root
	root := v.observation.Rows["Open_vSwitch"][rootID]
	out := candidate.QoSCreateState{
		Root: rootID, Allowed: maps.Clone(names), Names: map[string]uint64{},
		Objects: map[string]candidate.Binding{}, Capacity: MaxRows,
		Supported: v.observation.Schema.QoSCreate && apitypes.UUID(rootID) && root.UUID == rootID && qosLocalLabels(root),
	}
	for _, rows := range v.observation.Rows {
		out.Capacity -= len(rows)
	}
	encoded, err := json.Marshal(v.observation.Rows)
	if err != nil {
		out.Capacity = 0
	} else {
		out.Capacity = min(out.Capacity, (MaxSnapshotBytes-len(encoded))/MaxRowBytes)
	}
	for _, row := range v.observation.Rows["QoS"] {
		if name := qosProfileAlias(row); name != "" && names[name] {
			out.Names[name]++
		}
	}
	for _, want := range bindings {
		if (want.Table != "QoS" && want.Table != "Queue") || want.OVSUUID == "" {
			continue
		}
		if _, exists := v.observation.Rows[want.Table][want.OVSUUID]; exists {
			actual := v.decision.Bindings[Key(want.Table, want.OVSUUID)]
			if actual.State == "active" {
				out.Objects[want.OVSUUID] = candidate.Binding{Table: actual.Table, ManagementID: actual.ManagementID,
					OVSUUID: actual.UUID, Generation: v.decision.Generation}
			} else {
				// A row without an active identity still occupies the UUID.
				out.Objects[want.OVSUUID] = candidate.Binding{}
			}
		}
	}
	return out
}

func qosCreateStatus(v *view, names map[string]bool, fresh string, c authn.Claims) map[string]any {
	aliases := []string{}
	reason := "QOS_CREATION_AUTHORITY_REQUIRED"
	var generation, revision any
	for _, cap := range []string{candidate.QoSCapability, "workspace.write", "configuration.read"} {
		if !slices.Contains(c.Capabilities, cap) {
			return map[string]any{"available": false, "reason": "CAPABILITY_DENIED", "aliases": aliases,
				"instance_generation": nil, "revision": nil}
		}
	}
	state := qosCreateState(v, names, nil)
	switch {
	case fresh != "fresh":
		reason = "OVS_ASSOCIATION_STALE"
	case !state.Supported:
		reason = "QOS_CREATION_SCHEMA_UNSUPPORTED"
	case len(names) == 0:
	case state.Capacity < 2:
		reason = "QOS_CREATION_CAPACITY"
	default:
		for _, name := range slices.Sorted(maps.Keys(names)) {
			if state.Names[name] == 0 {
				aliases = append(aliases, name)
			}
		}
		if len(aliases) == 0 {
			reason = "QOS_CREATION_NAME_IN_USE"
		} else {
			reason = "QOS_CREATION_AVAILABLE"
			generation = v.decision.Generation
			revision = Digest([]any{state.Root, v.decision.Generation, v.observation.Schema.Digest, aliases})
		}
	}
	return map[string]any{"available": len(aliases) > 0, "reason": reason, "aliases": aliases,
		"instance_generation": generation, "revision": revision}
}
