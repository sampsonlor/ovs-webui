// Package evidence is the manager authority for durable jobs, events and audit.
// SQL-only methods participate in the caller's existing admission transaction.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	MaxJobs                = 100000
	MaxEvents              = 100000
	MaxAudit               = 500000
	MaxQueued              = 16
	MaxRunning             = 2
	PageBytes              = 40 << 10
	ReservedControlRecords = 128
)

type Request struct {
	Principal, Credential, Capability, Operation, Domain, Epoch, ID, Correlation string
}
type requestKey struct{}

func WithRequest(ctx context.Context, r Request) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}
func RequestFrom(ctx context.Context) Request { r, _ := ctx.Value(requestKey{}).(Request); return r }

type Job struct {
	ID              string        `json:"id"`
	Sequence        string        `json:"sequence"`
	State           string        `json:"state"`
	Operation       string        `json:"operation"`
	Owner           string        `json:"owner_id"`
	Capability      string        `json:"capability"`
	Resource        *apitypes.Ref `json:"resource_ref"`
	Correlation     string        `json:"correlation_id"`
	RequestID       string        `json:"request_id,omitempty"`
	RequestDomain   string        `json:"request_domain,omitempty"`
	RequestEpoch    string        `json:"request_epoch,omitempty"`
	Credential      string        `json:"credential_id,omitempty"`
	ChangeSet       string        `json:"changeset_id,omitempty"`
	Transaction     string        `json:"transaction_id,omitempty"`
	Cancellable     bool          `json:"cancellable"`
	CancelRequested bool          `json:"cancel_requested"`
	Dispatch        string        `json:"dispatch_state"`
	Business        string        `json:"business_outcome"`
	Commit          string        `json:"commit_outcome"`
	Applied         string        `json:"applied_outcome"`
	Confirmation    string        `json:"confirmation_state"`
	Reason          string        `json:"reason_code"`
	Handler         string        `json:"recovery_handler"`
	Created         time.Time     `json:"created_at"`
	Updated         time.Time     `json:"updated_at"`
	Completed       *time.Time    `json:"completed_at"`
}

// No free-text payload, credential material, native options, HTTP headers or
// command output has a serializable slot. Details are deliberately discarded.
type Record struct {
	ID            string         `json:"id"`
	Collection    string         `json:"resource_kind"`
	Sequence      string         `json:"sequence"`
	Origin        string         `json:"origin"`
	Actor         string         `json:"actor_id,omitempty"`
	Credential    string         `json:"credential_id,omitempty"`
	Capability    string         `json:"capability,omitempty"`
	Operation     string         `json:"operation"`
	Object        *apitypes.Ref  `json:"object_ref"`
	ChangeSet     string         `json:"changeset_id,omitempty"`
	Transaction   string         `json:"transaction_id,omitempty"`
	Job           string         `json:"job_id,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	RequestDomain string         `json:"request_domain,omitempty"`
	RequestEpoch  string         `json:"request_epoch,omitempty"`
	Correlation   string         `json:"correlation_id"`
	Result        string         `json:"result"`
	Reason        string         `json:"reason_code,omitempty"`
	Created       time.Time      `json:"created_at"`
	DedupKey      string         `json:"-"`
	Critical      bool           `json:"-"`
	Details       map[string]any `json:"-"`
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func code(s string) bool            { return codePattern.MatchString(s) }
func optionalID(s string) bool      { return s == "" || apitypes.ManagementID(s) }
func optionalRequest(s string) bool { return s == "" || apitypes.UUID(s) }
func validRef(r *apitypes.Ref) bool { return r == nil || code(r.Kind) && apitypes.ManagementID(r.ID) }
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}
func validOutcomes(j Job) bool {
	return slices.Contains([]string{"not-started", "prepared", "sent", "unknown", "completed"}, j.Dispatch) && slices.Contains([]string{"unknown", "success", "failure", "cancelled"}, j.Business) && slices.Contains([]string{"not-sent", "unknown", "committed", "rejected", "not-applicable"}, j.Commit) && slices.Contains([]string{"not-applicable", "unknown", "pending", "applied", "not-applied"}, j.Applied) && slices.Contains([]string{"not-applicable", "pending", "confirmed", "expired"}, j.Confirmation) && code(j.Reason)
}
func Operation(op string) bool {
	switch op {
	case "listJobs", "readJob", "listEvents", "readEvent", "listAudit", "readAudit", "exportJobs", "exportEvents", "exportAudit":
		return true
	}
	return false
}

// Only these already-authorized control operations may admit a new receipt/job
// into reserved space. This classification grants no permission by itself.
func ControlOperation(op string) bool {
	return op == "cancelJob" || op == "confirmCertificate" || op == "decideTransaction" || op == "reconcileTransaction"
}
