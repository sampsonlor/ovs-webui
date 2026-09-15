// Package execution owns the durable field-execution component. Public Apply
// admission and Safe Apply policy must supply a current workspace/safety lease;
// the component itself never manufactures a risk clearance or a confirmation.
package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

const MaxPlanBytes = 256 << 10
const PreflightFor = 5 * time.Second
const AppliedFor = 15 * time.Second
const MarkerKey = "ovs-webui.vlan-commit"

type Plan struct {
	ID         string             `json:"id"`
	Envelope   candidate.Envelope `json:"envelope"`
	Schema     string             `json:"schema"`
	Generation string             `json:"generation"`
	Root       string             `json:"root"`
	Marker     string             `json:"marker"`
	NextFloor  string             `json:"next_floor"`
	CurFloor   string             `json:"cur_floor"`
	Prepared   time.Time          `json:"prepared_at"`
	Native     json.RawMessage    `json:"native"`
}

type Outcome struct {
	Commit   string     `json:"commit_outcome"`
	Applied  string     `json:"applied_outcome"`
	Reason   string     `json:"reason_code"`
	Target   *string    `json:"target_next_cfg"`
	Current  *string    `json:"observed_cur_cfg"`
	Observed *time.Time `json:"observed_at"`
}

type Provider interface {
	Prepare(context.Context, string, string, candidate.Envelope) (Plan, error)
	// beforeSend must succeed immediately before mutation bytes are written.
	// It persists dispatch intent and repeats the current authorization/lease.
	Commit(context.Context, Plan, func() error) Outcome
	Observe(context.Context, Plan, Outcome) Outcome
}

type Request struct {
	ID           string             `json:"request_id"`
	ValidationID string             `json:"validation_id"`
	Envelope     candidate.Envelope `json:"envelope"`
}

// Authorization is produced by mgrd from the current credential and immutable
// Validation. It contains identifiers/digests only, never the bearer credential.
type Authorization struct {
	Owner, Credential, Epoch, Policy, ProviderPolicy, ChangeSet, Validation, Scope string
	Expires                                                                        time.Time
}

// Lease coordinates the web.db reservation and the independent safety domain.
// Check must reload the frozen workspace and validate its durable reservation.
// It is required again immediately before dispatch. A nil lease fails closed.
type Lease interface {
	Check(context.Context, Request) error
}

type Record struct {
	ID                string        `json:"id"`
	Sequence          string        `json:"sequence"`
	State             string        `json:"state"`
	Owner             string        `json:"owner_id"`
	RequestID         string        `json:"request_id"`
	JobID             string        `json:"job_id"`
	Correlation       string        `json:"correlation_id"`
	ValidationID      string        `json:"validation_id"`
	CandidateID       string        `json:"candidate_id"`
	CandidateRevision string        `json:"candidate_revision"`
	Generation        string        `json:"instance_generation"`
	Created           time.Time     `json:"created_at"`
	Updated           time.Time     `json:"updated_at"`
	Outcome           Outcome       `json:"outcome"`
	Authorization     Authorization `json:"-"`
	Plan              Plan          `json:"-"`
}
