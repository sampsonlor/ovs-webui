package executions

import (
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
)

// Only direct MTU targets from the admitted, provider-prepared envelope are
// associated. Parents, default contributors and same-name replacements are
// not targets. Older journals without this context retain limited coverage.
func mtuEvidenceObjects(r execution.Record) []apitypes.Ref {
	refs := []apitypes.Ref{}
	for _, i := range r.Plan.Envelope.Candidate.Intents {
		if i.MTU != nil && candidate.IsMTUOperation(i.Operation) && i.Object.Table == "Interface" && apitypes.ManagementID(i.Object.ManagementID) {
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
