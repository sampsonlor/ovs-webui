package inventory

import (
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// This slice permits QinQ on one existing system/dummy Interface under one
// root-reachable system Bridge. TPID is observed and preserved, never rewritten.
func projectQinQ(v *view, row, bridge Row, p *candidate.Port) {
	config, known := row.Values["other_config"].(map[string]any)
	q := &candidate.QinQContext{}
	if value, exists := config["qinq-ethtype"]; exists {
		text, ok := value.(string)
		known = known && ok && slices.Contains([]string{"802.1ad", "802.1q"}, text)
		q.EtherType = &text
	}
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	known = known && len(v.observation.Rows["Open_vSwitch"]) == 1 && root.UUID != "" && slices.Contains(refs(root.Values["bridges"]), bridge.UUID)
	datapath, ok := bridge.Values["datapath_type"].(string)
	known = known && ok && slices.Contains([]string{"", "system", "dummy"}, datapath) && row.Values["name"] != bridge.Values["name"]
	ids := refs(row.Values["interfaces"])
	known = known && len(ids) == 1
	members := map[string]any{}
	for _, id := range ids {
		member := v.observation.Rows["Interface"][id]
		binding := v.decision.Bindings[Key("Interface", id)]
		kind, kindOK := member.Values["type"].(string)
		options, optionsOK := member.Values["options"].(map[string]any)
		parents := 0
		for _, other := range v.observation.Rows["Port"] {
			if slices.Contains(refs(other.Values["interfaces"]), id) {
				parents++
			}
		}
		known = known && member.UUID != "" && binding.State == "active" && parents == 1 && kindOK && slices.Contains([]string{"", "system", "dummy"}, kind) && optionsOK && len(options) == 0
		members[id] = []any{binding.ManagementID, parents}
	}
	q.Dependency = Digest([]any{root.UUID, bridge.UUID, members, known, q.EtherType})
	p.QinQ, p.QinQSupported = q, known
}
