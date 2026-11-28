package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/configtransfer"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	checkpointFormat   = "ovs-webui.rolling-vlan-checkpoint.v1"
	checkpointFileName = "last-known-good-vlan.json"
	maxCheckpointBytes = MaxBackupPairManifestBytes
	checkpointScope    = "confirmed-manager-transaction"
)

var ErrCheckpointScope = errors.New("CHECKPOINT_SCOPE_UNSUPPORTED")

type VLANCheckpoint struct {
	Manifest VLANCheckpointManifest  `json:"manifest"`
	Recovery configtransfer.Document `json:"recovery"`
	SHA256   string                  `json:"sha256"`
}

type VLANCheckpointManifest struct {
	Format             string    `json:"format"`
	EvidenceScope      string    `json:"evidence_scope"`
	SourceDatabaseID   string    `json:"source_database_id"`
	SourceSchema       int       `json:"source_schema"`
	SourceSoftware     string    `json:"source_software"`
	ConfirmedAt        time.Time `json:"confirmed_at"`
	TransactionID      string    `json:"transaction_id"`
	InstanceGeneration string    `json:"instance_generation"`
}

type vlanCheckpointSource struct {
	manifest     VLANCheckpointManifest
	root, schema string
	document     []byte
}

// The retained native plan can contain opaque provider data. Narrow the decode
// to the specific read guards that prove shape and discard the original bytes.
type checkpointPlan struct {
	ID, Root, Schema, Generation string
	Envelope                     struct {
		Candidate struct {
			Generation string `json:"instance_generation"`
			Intents    []struct {
				Operation string
				Object    candidate.Binding
				Value     candidate.VLAN
				Schema    string `json:"schema_digest"`
			}
		}
	}
	Native struct {
		Operations []struct {
			Op, Table string
			Rows      []checkpointGuardRow
		}
	}
}

type checkpointGuardRow struct {
	UUID       []string        `json:"_uuid"`
	Name       string          `json:"name"`
	Type       *string         `json:"type"`
	Interfaces json.RawMessage `json:"interfaces"`
}

func checkpointNativeScope(plan checkpointPlan, port candidate.Binding) (string, configtransfer.PortShape, bool) {
	var portRow, memberRow, bridgeRow checkpointGuardRow
	var ports, members, bridges int
	for _, op := range plan.Native.Operations {
		if op.Op != "wait" || len(op.Rows) != 1 {
			continue
		}
		row := op.Rows[0]
		switch op.Table {
		case "Port":
			if checkpointUUID(row.UUID) == port.OVSUUID {
				portRow = row
				ports++
			}
		case "Interface":
			memberRow = row
			members++
		case "Bridge":
			bridgeRow = row
			bridges++
		}
	}
	memberUUID := checkpointUUID(memberRow.UUID)
	if ports != 1 || members != 1 || bridges != 1 || checkpointUUID(bridgeRow.UUID) == "" ||
		portRow.Name == "" || bridgeRow.Name == "" || bridgeRow.Name == portRow.Name ||
		memberRow.Type == nil || memberUUID == "" || checkpointSoleMember(portRow.Interfaces) != memberUUID {
		return "", "", false
	}
	switch *memberRow.Type {
	case "internal":
		return memberUUID, configtransfer.StandaloneInternal, true
	case "", "system":
		return memberUUID, configtransfer.StandaloneSystem, true
	default:
		return "", "", false
	}
}

func checkpointUUID(value []string) string {
	if len(value) != 2 || value[0] != "uuid" || !apitypes.UUID(value[1]) || strings.ToLower(value[1]) != value[1] {
		return ""
	}
	return value[1]
}

func checkpointSoleMember(data json.RawMessage) string {
	var pair []json.RawMessage
	var marker string
	var uuids [][]string
	if json.Unmarshal(data, &pair) != nil || len(pair) != 2 ||
		json.Unmarshal(pair[0], &marker) != nil || marker != "set" ||
		json.Unmarshal(pair[1], &uuids) != nil || len(uuids) != 1 {
		return ""
	}
	return checkpointUUID(uuids[0])
}

func projectVLANCheckpoint(source vlanCheckpointSource, binding func(table, nativeID string) (candidate.Binding, error)) (VLANCheckpoint, error) {
	var plan checkpointPlan
	if len(source.document) == 0 || json.Unmarshal(source.document, &plan) != nil ||
		plan.ID != source.manifest.TransactionID || plan.Generation != source.manifest.InstanceGeneration ||
		plan.Envelope.Candidate.Generation != plan.Generation || plan.Root != source.root || !apitypes.UUID(plan.Root) ||
		plan.Schema != source.schema || !validPairDigest(plan.Schema) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	if len(plan.Envelope.Candidate.Intents) != 1 {
		return VLANCheckpoint{}, ErrCheckpointScope
	}
	intent := plan.Envelope.Candidate.Intents[0]
	value := intent.Value
	if intent.Operation != "port.vlan.set" || intent.Schema != plan.Schema || intent.Object.Table != "Port" ||
		intent.Object.Generation != plan.Generation || value.Mode == nil || *value.Mode != "access" ||
		value.Tag == nil || *value.Tag < 1 || *value.Tag > 4094 || len(value.Trunks) != 0 || len(value.CVLANs) != 0 {
		return VLANCheckpoint{}, ErrCheckpointScope
	}
	port, err := binding("Port", intent.Object.OVSUUID)
	if err != nil || port != intent.Object {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	memberUUID, shape, ok := checkpointNativeScope(plan, port)
	if !ok {
		return VLANCheckpoint{}, ErrCheckpointScope
	}
	member, err := binding("Interface", memberUUID)
	if err != nil || member.Table != "Interface" || member.OVSUUID != memberUUID || member.Generation != plan.Generation {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	tag, trunks := []int{*value.Tag}, []int{}
	result := VLANCheckpoint{
		Manifest: source.manifest,
		Recovery: configtransfer.Document{
			Format: configtransfer.FormatV1, Generation: plan.Generation, SchemaDigest: plan.Schema,
			Ports: []configtransfer.PortDocument{{Binding: port, Member: member, Shape: shape,
				VLAN: &configtransfer.VLANFields{Mode: value.Mode, Tag: &tag, Trunks: &trunks}}},
		},
	}
	result.SHA256 = checkpointDigest(result)
	if !validVLANCheckpoint(result) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	return result, nil
}

func checkpointDigest(checkpoint VLANCheckpoint) string {
	body, _ := json.Marshal(struct {
		Manifest VLANCheckpointManifest  `json:"manifest"`
		Recovery configtransfer.Document `json:"recovery"`
	}{checkpoint.Manifest, checkpoint.Recovery})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func validVLANCheckpoint(checkpoint VLANCheckpoint) bool {
	manifest, recovery := checkpoint.Manifest, checkpoint.Recovery
	if manifest.Format != checkpointFormat || manifest.EvidenceScope != checkpointScope || !apitypes.ManagementID(manifest.SourceDatabaseID) ||
		!apitypes.ManagementID(manifest.TransactionID) || !apitypes.ManagementID(manifest.InstanceGeneration) ||
		manifest.SourceSchema <= 0 || !repository.ValidID(manifest.SourceSoftware) || manifest.ConfirmedAt.IsZero() ||
		recovery.Generation != manifest.InstanceGeneration || len(recovery.Ports) != 1 ||
		checkpoint.SHA256 != checkpointDigest(checkpoint) {
		return false
	}
	fields := recovery.Ports[0].VLAN
	if fields == nil || fields.Mode == nil || *fields.Mode != "access" ||
		fields.Tag == nil || len(*fields.Tag) != 1 || fields.Trunks == nil || len(*fields.Trunks) != 0 {
		return false
	}
	body, _ := json.Marshal(recovery)
	_, err := configtransfer.Decode(body)
	return err == nil
}

func encodeVLANCheckpoint(checkpoint VLANCheckpoint) ([]byte, error) {
	data, err := boundedPairJSON(checkpoint)
	if err != nil || len(data) > maxCheckpointBytes {
		return nil, repository.ErrUnavailable
	}
	return data, nil
}

func decodeVLANCheckpoint(data []byte) (VLANCheckpoint, error) {
	var checkpoint VLANCheckpoint
	if len(data) == 0 || len(data) > maxCheckpointBytes ||
		json.Unmarshal(data, &checkpoint) != nil || !validVLANCheckpoint(checkpoint) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	encoded, err := encodeVLANCheckpoint(checkpoint)
	if err != nil || !bytes.Equal(encoded, data) {
		return VLANCheckpoint{}, repository.ErrUnavailable
	}
	return checkpoint, nil
}
