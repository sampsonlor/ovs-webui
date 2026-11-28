package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	plan "github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/configtransfer"
)

const (
	importVLANOperation = "importVLANConfiguration"
	vlanTransferBytes   = 24 << 10
	vlanTransferPorts   = 8
)

type vlanImportRequest struct {
	Document   string `json:"document_base64"`
	Generation string `json:"expected_generation"`
	Mode       string `json:"mode"`
	Mappings   []struct {
		Source      string `json:"source_id"`
		Destination string `json:"destination_id"`
		Member      string `json:"destination_member_id"`
	} `json:"mappings"`
}

func canStageVLANImport(claims authn.Claims) bool {
	for _, capability := range []string{"configuration.import", "workspace.write", "configuration.read", "inventory.read", "ovs.port.vlan.write"} {
		if !slices.Contains(claims.Capabilities, capability) {
			return false
		}
	}
	return true
}

func (r *Repository) buildVLANImport(ctx context.Context, claims authn.Claims, e plan.Envelope, requestID string, body []byte) (plan.Command, plan.Snapshot, error) {
	invalid := func(code string) (plan.Command, plan.Snapshot, error) {
		return plan.Command{}, plan.Snapshot{}, apitypes.Fail(422, code)
	}
	var request vlanImportRequest
	if json.Unmarshal(body, &request) != nil || !apitypes.ManagementID(request.Generation) {
		return invalid("CONFIG_DOCUMENT_INVALID")
	}
	planTransfer := configtransfer.PlanMerge
	exact := request.Mode == "exact-restore"
	if exact {
		planTransfer = configtransfer.PlanExactRestore
	}
	document, err := base64.StdEncoding.Strict().DecodeString(request.Document)
	if err != nil || len(document) > vlanTransferBytes {
		return invalid("CONFIG_DOCUMENT_LIMIT")
	}
	source, err := configtransfer.Decode(document)
	if err != nil {
		return plan.Command{}, plan.Snapshot{}, err
	}
	if len(source.Ports) == 0 || len(source.Ports) > vlanTransferPorts || len(request.Mappings) > vlanTransferPorts {
		return invalid("CONFIG_SCOPE_INVALID")
	}
	mappings := make([]configtransfer.Mapping, 0, len(request.Mappings))
	destinations := make(map[string]string, len(request.Mappings))
	for _, row := range request.Mappings {
		if !apitypes.ManagementID(row.Source) || !apitypes.ManagementID(row.Destination) || destinations[row.Source] != "" {
			return invalid("CONFIG_MAPPING_INVALID")
		}
		mappings = append(mappings, configtransfer.Mapping{SourceID: row.Source, DestinationID: row.Destination, DestinationMemberID: row.Member})
		destinations[row.Source] = row.Destination
	}
	ports := make([]string, 0, len(source.Ports))
	for _, row := range source.Ports {
		id := row.Binding.ManagementID
		if matched := destinations[id]; matched != "" {
			id = matched
		} else if exact || source.Generation != request.Generation {
			return invalid("CONFIG_MAPPING_REQUIRED")
		}
		ports = append(ports, id)
	}
	var bindings []plan.Binding
	err = r.store.Read(ctx, func(ctx context.Context, q *sql.Conn) error {
		var err error
		bindings, err = vlanTransferBindings(ctx, q, claims, ports)
		return err
	})
	if err != nil {
		return plan.Command{}, plan.Snapshot{}, err
	}
	current, err := r.candidateSnapshot(ctx, append(plan.Bindings(e.Candidate), bindings...))
	if err != nil {
		return plan.Command{}, plan.Snapshot{}, err
	}
	transfer, err := planTransfer(document, request.Generation, current, mappings)
	if err != nil {
		return plan.Command{}, plan.Snapshot{}, err
	}
	intents, err := transfer.CandidateIntents(current)
	if err != nil {
		return plan.Command{}, plan.Snapshot{}, err
	}
	if len(intents) == 0 {
		return plan.Command{}, plan.Snapshot{}, apitypes.Fail(409, "CONFIG_NO_CHANGES")
	}
	for _, next := range intents {
		if slices.ContainsFunc(e.Candidate.Intents, func(held plan.StoredIntent) bool {
			return held.Operation == "port.vlan.set" && held.Object.ManagementID == next.Object.ManagementID
		}) {
			return plan.Command{}, plan.Snapshot{}, apitypes.Fail(409, "CANDIDATE_IMPORT_OVERLAP")
		}
	}
	return plan.Command{RequestID: requestID, Operation: "stage", Intents: intents}, current, nil
}

func vlanTransferBindings(ctx context.Context, q querier, claims authn.Claims, ids []string) ([]plan.Binding, error) {
	if len(ids) == 0 || len(ids) > vlanTransferPorts {
		return nil, apitypes.Fail(422, "CONFIG_SCOPE_INVALID")
	}
	refs := make([]apitypes.Ref, len(ids))
	for i, id := range ids {
		refs[i] = apitypes.Ref{Kind: "port", ID: id}
	}
	if err := checkRefs(ctx, q, claims, refs); err != nil {
		return nil, err
	}
	out := make([]plan.Binding, 0, len(ids))
	for _, id := range ids {
		binding := plan.Binding{ManagementID: id, Table: "Port"}
		err := q.QueryRowContext(ctx, `SELECT i.ovs_uuid,i.generation FROM identities i
          JOIN inventory_state s ON s.generation=i.generation
          WHERE i.management_id=? AND i.table_name='Port' AND i.state='active' AND s.state='confirmed'`, id).Scan(&binding.OVSUUID, &binding.Generation)
		if err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, nil
}

func (r *Repository) readVLANExport(ctx context.Context, q querier, claims authn.Claims, scope string) (any, error) {
	if err := require(claims, "configuration.read", false, 0); err != nil {
		return nil, err
	}
	ports := strings.Split(scope, ",")
	bindings, err := vlanTransferBindings(ctx, q, claims, ports)
	if err != nil {
		return nil, err
	}
	current, err := r.candidateSnapshot(ctx, bindings)
	if err != nil {
		return nil, err
	}
	document, err := configtransfer.Export(current, ports)
	if err != nil {
		return nil, err
	}
	if len(document) > vlanTransferBytes {
		return nil, apitypes.Fail(422, "CONFIG_DOCUMENT_LIMIT")
	}
	hash := sha256.Sum256(document)
	return map[string]string{"document_base64": base64.StdEncoding.EncodeToString(document), "sha256": hex.EncodeToString(hash[:])}, nil
}
