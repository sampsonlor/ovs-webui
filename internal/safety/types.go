// Package safety defines the typed, private Safe Apply coordination protocol.
package safety

import (
	"context"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

const ConfirmationWindow = 120 * time.Second
const PreparationBudget = 30 * time.Second
const EvidenceFreshFor = 4 * time.Second

type Provider interface {
	execution.Provider
	PrepareRollback(context.Context, execution.Plan, string) (execution.Plan, error)
}

// Probe targets come only from reviewed root configuration, never HTTP/IPC.
type Probe interface {
	Domain() string
	Check(context.Context) error
}

type Request struct {
	Envelope    candidate.Envelope `json:"envelope"`
	Command     authn.Command      `json:"command"`
	Reservation string             `json:"reservation_id"`
}
type Resolution struct {
	Pending     bool                `json:"pending"`
	Transaction *apitypes.Ref       `json:"transaction"`
	Envelope    *candidate.Envelope `json:"envelope"`
}
type Manager interface {
	AdmitSafeApply(context.Context, string, Request) (apitypes.Result, error)
	ResolveSafeApply(context.Context, string, Request) (Resolution, error)
}

type Deadline struct {
	Boot    string    `json:"boot_id"`
	StartNS int64     `json:"start_ns"`
	EndNS   int64     `json:"deadline_ns"`
	Started time.Time `json:"started_at"`
	Wall    time.Time `json:"deadline_wall"`
}

func NewDeadline(c tlscontrol.Clock, duration time.Duration) Deadline {
	return Deadline{c.BootID, c.NS, c.NS + int64(duration), c.Wall.UTC(), c.Wall.Add(duration).UTC()}
}
func (d Deadline) Expired(c tlscontrol.Clock) bool {
	return d.Boot == "" || c.BootID != d.Boot || c.NS < d.StartNS || c.NS >= d.EndNS || c.Wall.Before(d.Started) || !c.Wall.Before(d.Wall)
}

// Record is private durable journal data. Public views deliberately omit plans,
// workspace envelopes, probe addresses and internal reservation identifiers.
type Record struct {
	LastWall      time.Time         `json:"last_wall"`
	State         string            `json:"state"`
	Reason        string            `json:"reason"`
	Domain        string            `json:"domain"`
	Reservation   string            `json:"reservation_id"`
	Preparation   Deadline          `json:"preparation"`
	Confirmation  *Deadline         `json:"confirmation"`
	HealthyAt     *time.Time        `json:"healthy_at"`
	ProbeFailures int               `json:"probe_failures"`
	Rollback      *execution.Plan   `json:"rollback_plan"`
	Outcome       execution.Outcome `json:"rollback_outcome"`
}

func Terminal(state string) bool {
	return state == "confirmed" || state == "rolled-back" || state == "not-committed"
}
