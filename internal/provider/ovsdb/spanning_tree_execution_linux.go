//go:build linux

package ovsdb

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

func treeWitnessAuthority(p SpanningTreePreflight) string {
	if p.Witness == nil {
		return ""
	}
	s := p.Witness.Second
	w := s.Witness
	if p.journalAuthority == "" {
		return ""
	}
	return candidate.Digest([]any{p.journalAuthority, s.buildIdentity, s.buildSchema, s.Daemon, w.Daemon, w.Database.RootUUID, w.Database.Authority, w.Bridge.UUID, w.Bridge.Instance, w.Bridge.Epoch})
}

func (e *Executor) treeExecutionEvidence(ctx context.Context, i candidate.StoredIntent, check func(context.Context) error) (out SpanningTreePreflight, err error) {
	if e.spanningTree == nil {
		return SpanningTreePreflight{}, errors.New("SPANNING_TREE_WITNESS_NOT_CONFIGURED")
	}
	o := e.spanningTree
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	trace := &treeEvidenceTrace{stage: "authorization"}
	ctx = context.WithValue(ctx, treeEvidenceTraceKey{}, trace)
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			err = &treeEvidenceFailure{stage: trace.stage, cause: err}
		}
	}()
	if check == nil {
		return SpanningTreePreflight{}, witnessUnproven
	}
	if err := check(ctx); err != nil {
		return SpanningTreePreflight{}, err
	}
	treeEvidenceStep(ctx, "control-source")
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", o.ControlSocket)
	if err != nil {
		return SpanningTreePreflight{}, witnessUnproven
	}
	daemon, err := spanningTreeDaemon(conn, o.PeerUID)
	_ = conn.Close()
	if err != nil {
		return SpanningTreePreflight{}, witnessUnproven
	}
	if err = check(ctx); err != nil {
		return SpanningTreePreflight{}, err
	}
	return e.ReadSpanningTreeWitnessPreflight(ctx, i, SpanningTreeControl{Socket: o.ControlSocket, PeerUID: o.PeerUID, ExpectedDaemon: daemon}, SpanningTreeWitnessBuild{Executable: o.Executable, Manifest: o.Manifest, ManifestDigest: o.ManifestDigest}, check)
}

func (e *Executor) prepareTreeExecution(ctx context.Context, p execution.Plan) (execution.Plan, error) {
	i, ok := spanningTreeIntent(p.Envelope.Candidate)
	if !ok {
		return p, nil
	}
	proof, err := e.treeExecutionEvidence(ctx, i, execution.CheckAdmission)
	if err != nil {
		var problem *apitypes.Problem
		if errors.As(err, &problem) {
			return execution.Plan{}, err
		}
		return execution.Plan{}, apitypes.Fail(409, "SPANNING_TREE_EXECUTION_EVIDENCE_UNPROVEN")
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil {
		return execution.Plan{}, errors.New("SPANNING_TREE_PLAN_INVALID")
	}
	n.TreeAuthority = treeWitnessAuthority(proof)
	if n.TreeAuthority == "" {
		return execution.Plan{}, witnessUnproven
	}
	p.Native, err = json.Marshal(n)
	if err != nil || len(p.Native) > execution.MaxPlanBytes {
		return execution.Plan{}, errors.New("SPANNING_TREE_PLAN_BUDGET")
	}
	return p, nil
}

func (e *Executor) treeDispatchCheck(ctx context.Context, p execution.Plan, n nativePlan) error {
	i, ok := spanningTreeIntent(p.Envelope.Candidate)
	if !ok {
		return nil
	}
	proof, err := e.treeExecutionEvidence(ctx, i, execution.CheckAdmission)
	if err != nil {
		return err
	}
	if n.TreeAuthority == "" || treeWitnessAuthority(proof) != n.TreeAuthority {
		return witnessUnproven
	}
	return execution.CheckAdmission(ctx)
}

func (e *Executor) treeAppliedProof(ctx context.Context, p execution.Plan, v inventory.ExecutionView, target *string) error {
	i, ok := spanningTreeIntent(p.Envelope.Candidate)
	if !ok {
		return nil
	}
	if target == nil {
		return witnessUnproven
	}
	candidate.AfterImage(&i)
	// Durable manager recovery is observation-only. It may inspect its own
	// checkpoint after credential loss; root grants, immutable binding, build,
	// native fences and current source are still checked around every read.
	proof, err := e.treeExecutionEvidence(ctx, i, func(c context.Context) error { return c.Err() })
	if err != nil {
		return err
	}
	var n nativePlan
	if json.Unmarshal(p.Native, &n) != nil || n.TreeAuthority == "" || n.TreePolicy != v.Candidate.Policy || treeWitnessAuthority(proof) != n.TreeAuthority || !treeTargetReached(proof, target) {
		return witnessUnproven
	}
	return nil
}

func treeTargetReached(proof SpanningTreePreflight, target *string) bool {
	if proof.Witness == nil || target == nil {
		return false
	}
	a, err := strconv.ParseInt(proof.Witness.Second.Witness.Configuration.NextCfg, 10, 64)
	b, e2 := strconv.ParseInt(*target, 10, 64)
	return err == nil && e2 == nil && a >= b
}
