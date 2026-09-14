// Package tlscontrol defines the private certificate consumer protocol. Private
// keys never cross this protocol; mgrd authorizes bounded metadata and leases.
package tlscontrol

import (
	"context"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"time"
)

const RecoveryWindow = 120 * time.Second

type Descriptor struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Hostname    string    `json:"hostname"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	InputDigest string    `json:"input_digest"`
}
type Command struct {
	Method       string      `json:"method"`
	URI          string      `json:"uri"`
	Epoch        string      `json:"epoch"`
	RequestID    string      `json:"request_id"`
	Precondition string      `json:"precondition"`
	Candidate    *Descriptor `json:"candidate,omitempty"`
	ServedID     string      `json:"served_id,omitempty"`
}
type State struct {
	Revision     string `json:"revision"`
	ActiveID     string `json:"active_id"`
	TrialID      string `json:"trial_id"`
	JobID        string `json:"job_id"`
	BootID       string `json:"boot_id"`
	StartedNS    int64  `json:"started_ns"`
	DeadlineNS   int64  `json:"deadline_ns"`
	DeadlineWall int64  `json:"deadline_wall"`
}
type Manager interface {
	ExecuteTLS(context.Context, string, Command) (apitypes.Result, error)
	TLSState(context.Context) (State, error)
}
type Clock struct {
	BootID string
	NS     int64
	Wall   time.Time
}

func (s State) Expired(c Clock) bool {
	return s.TrialID != "" && (c.BootID == "" || c.BootID != s.BootID || c.NS < s.StartedNS || c.NS >= s.DeadlineNS || c.Wall.Unix() >= s.DeadlineWall || c.Wall.Unix() < s.DeadlineWall-int64(RecoveryWindow.Seconds()))
}
