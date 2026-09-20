// Package candidate defines bounded, typed configuration intent. It has no
// provider write operation and does not depend on HTTP, SQLite or generated DTOs.
package candidate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const MaxIntents = 32
const MaxDocument = 40 << 10
const ValidFor = 300 * time.Second
const ValidatorVersion = "port-vlan-v1"

type Binding struct {
	ManagementID string `json:"management_id"`
	OVSUUID      string `json:"ovs_uuid"`
	Table        string `json:"table"`
	Generation   string `json:"instance_generation"`
}
type VLAN struct {
	Mode   *string `json:"vlan_mode"`
	Tag    *int    `json:"tag"`
	Trunks []int   `json:"trunks"`
	CVLANs []int   `json:"cvlans"`
}
type Intent struct {
	ID        string  `json:"intent_id"`
	Operation string  `json:"operation"`
	Object    Binding `json:"object"`
	Value     VLAN    `json:"value"`
}
type StoredIntent struct {
	// IPC fields are explicit: strict request decoding deliberately does not
	// infer encoding/json's anonymous-field promotion rules.
	ID         string  `json:"intent_id"`
	Operation  string  `json:"operation"`
	Object     Binding `json:"object"`
	Value      VLAN    `json:"value"`
	Before     VLAN    `json:"before"`
	Dependency string  `json:"dependency_revision"`
	Schema     string  `json:"schema_digest"`
}
type Candidate struct {
	ID           string         `json:"id"`
	Revision     string         `json:"revision"`
	Generation   *string        `json:"instance_generation"`
	BaseRevision *string        `json:"base_config_revision"`
	State        string         `json:"state"`
	Intents      []StoredIntent `json:"intents"`
	Consumed     *apitypes.Ref  `json:"consumed_by"`
}

// Envelope is private IPC/storage data. Seal authenticates mgrd-captured
// originals; neither a browser nor a modified web.db may invent a before value.
type Envelope struct {
	Owner     string    `json:"owner_id"`
	Epoch     string    `json:"workspace_epoch"`
	Sequence  int64     `json:"sequence"`
	Candidate Candidate `json:"candidate"`
	Seal      string    `json:"seal"`
}
type Command struct {
	RequestID        string       `json:"request_id"`
	Operation        string       `json:"operation"`
	Intents          []Intent     `json:"intents,omitempty"`
	IntentIDs        []string     `json:"intent_ids,omitempty"`
	Generation       string       `json:"instance_generation,omitempty"`
	ConfigRevision   string       `json:"current_config_revision,omitempty"`
	ConflictSnapshot *string      `json:"conflict_snapshot_id,omitempty"`
	Resolutions      []Resolution `json:"resolutions,omitempty"`
}
type Resolution struct {
	IntentID string `json:"intent_id"`
	Choice   string `json:"choice"`
}
type Gate struct {
	Code     string `json:"code"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
	IntentID string `json:"intent_id,omitempty"`
}
type Diff struct {
	Object    Binding `json:"object"`
	Field     string  `json:"field"`
	Before    any     `json:"before"`
	After     any     `json:"after"`
	Authority string  `json:"authority"`
	Current   any     `json:"current"`
	Operation string  `json:"operation"`
	IntentID  string  `json:"intent_id"`
	Conflict  bool    `json:"conflict"`
}
type View struct {
	Candidate
	SafeApplyAvailable bool    `json:"safe_apply_available"`
	CurrentGeneration  *string `json:"current_instance_generation"`
	CurrentRevision    *string `json:"current_config_revision"`
	ConflictSnapshot   *string `json:"conflict_snapshot_id"`
	Diff               []Diff  `json:"diff"`
	Checks             []Gate  `json:"checks"`
	DiffTruncated      bool    `json:"diff_truncated"`
}

// Snapshot is one coherent, immutable projection of mgrd's current monitor.
// Dependencies contain only digests of the relevant structural/native fields.
type Port struct {
	Binding                       Binding
	VLAN                          VLAN
	Known, SchemaSupported        bool
	Modes                         []string
	Dependency, Authority, Reason string
}
type Snapshot struct {
	Generation, Revision, Schema, Policy string
	Ports                                map[string]Port
}
type Provider interface {
	CandidateSnapshot(context.Context, []Binding) (Snapshot, error)
}
type Validation struct {
	ID                string        `json:"id"`
	CandidateID       string        `json:"candidate_id"`
	CandidateRevision string        `json:"candidate_revision"`
	Generation        string        `json:"instance_generation"`
	ConfigRevision    string        `json:"config_revision"`
	PolicyRevision    string        `json:"policy_revision"`
	Expires           time.Time     `json:"expires_at"`
	Job               *apitypes.Ref `json:"job_ref"`
	State             string        `json:"state"`
	Checks            []Gate        `json:"checks"`
	Diff              []Diff        `json:"diff"`
	ChangeSetID       string        `json:"changeset_id"`
	Usable            bool          `json:"usable"`
	Invalidations     []Gate        `json:"invalidations"`
	Risk              string        `json:"risk"`
	ExecutionReady    bool          `json:"execution_ready"`
}
type PrepareRequest struct {
	Envelope Envelope      `json:"envelope"`
	Command  authn.Command `json:"command"`
}
type ValidateRequest struct {
	Envelope Envelope      `json:"envelope"`
	Command  authn.Command `json:"command"`
}
type ReadRequest struct {
	Envelope     Envelope `json:"envelope"`
	ValidationID string   `json:"validation_id,omitempty"`
}
type Manager interface {
	PrepareCandidate(context.Context, string, PrepareRequest) (Envelope, error)
	ReadCandidate(context.Context, string, ReadRequest) (authn.Response, error)
	ValidateCandidate(context.Context, string, ValidateRequest) (apitypes.Result, error)
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (e Envelope) signature(key []byte) string {
	e.Seal = ""
	b, _ := json.Marshal(e)
	h := hmac.New(sha256.New, key)
	h.Write([]byte("ovs-candidate-original-v1\x00"))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func (e *Envelope) Sign(key []byte) { e.Seal = e.signature(key) }
func (e Envelope) Verify(key []byte, owner string) error {
	c := e.Candidate
	if len(key) != 32 || e.Owner != owner || !apitypes.ManagementID(e.Epoch) || !apitypes.ManagementID(c.ID) || !apitypes.ManagementID(c.Revision) || e.Sequence < 0 || len(c.Intents) > MaxIntents {
		return apitypes.Fail(422, "INVALID_CANDIDATE")
	}
	if e.Sequence == 0 && e.Seal == "" && len(c.Intents) == 0 && c.State == "empty" && c.Generation == nil && c.BaseRevision == nil && c.Consumed == nil {
		return nil
	}
	if !hmac.Equal([]byte(e.Seal), []byte(e.signature(key))) {
		return apitypes.Fail(409, "CANDIDATE_INTEGRITY_FAILED")
	}
	return nil
}
func Bindings(c Candidate) []Binding {
	out := []Binding{}
	for _, i := range c.Intents {
		out = append(out, i.Object)
	}
	return out
}
func Budget(v any) error {
	b, err := json.Marshal(v)
	if err != nil || len(b) > MaxDocument {
		return apitypes.Fail(429, "CANDIDATE_BUDGET_EXCEEDED")
	}
	return nil
}
