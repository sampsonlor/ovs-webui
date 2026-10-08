package executions

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
)

// Field changes associate direct Interface targets only. A topology change
// associates its admitted original/desired graph, never reserved replacements.
// Older journals without this context retain limited coverage.
func interfaceEvidenceObjects(r execution.Record) []apitypes.Ref {
	refs := []apitypes.Ref{}
	for _, i := range r.Plan.Envelope.Candidate.Intents {
		if i.Topology != nil {
			for _, n := range append(append([]candidate.TopologyNode{}, i.Topology.Before...), i.Topology.After...) {
				ref := apitypes.Ref{Kind: map[string]string{"Bridge": "bridge", "Port": "port", "Interface": "interface"}[n.Binding.Table], ID: n.Binding.ManagementID}
				duplicate := false
				for _, p := range refs {
					if p == ref {
						duplicate = true
					}
				}
				if !duplicate {
					refs = append(refs, ref)
				}
			}
			continue
		}
		if (i.MTU != nil && candidate.IsMTUOperation(i.Operation) || i.Policing != nil && i.Operation == candidate.InterfacePolicingSet) && i.Object.Table == "Interface" && apitypes.ManagementID(i.Object.ManagementID) {
			ref := apitypes.Ref{Kind: "interface", ID: i.Object.ManagementID}
			duplicate := false
			for _, prior := range refs {
				if prior == ref {
					duplicate = true
					break
				}
			}
			if !duplicate {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}
