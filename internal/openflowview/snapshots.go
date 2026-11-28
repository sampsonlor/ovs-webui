// Package openflowview retains short-lived viewer snapshots from one qualified
// local OpenFlow query.
package openflowview

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	TTL               = 30 * time.Second
	MaxSnapshotBytes  = 40 << 10
	MaxPageRows       = 25
	maxSnapshots      = 32
	maxOwnerSnapshots = 4
)

type Scope struct {
	Principal, Credential, PermissionRevision string
}

func (s Scope) valid() bool {
	return apitypes.ManagementID(s.Principal) && apitypes.ManagementID(s.Credential) &&
		apitypes.ManagementID(s.PermissionRevision)
}

type Reference struct {
	ID         string    `json:"id"`
	BridgeID   string    `json:"bridge_id"`
	Generation string    `json:"instance_generation"`
	ObservedAt time.Time `json:"observed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Rows       int       `json:"retained_rows"`
	Truncated  bool      `json:"truncated"`
}

type record struct {
	scope Scope
	ref   Reference
	data  []byte
}

type Snapshots struct {
	mu      sync.Mutex
	entries map[string]record
}

func New() *Snapshots {
	return &Snapshots{entries: make(map[string]record)}
}

func (s *Snapshots) Put(now time.Time, scope Scope, snapshot ovsdb.OpenFlowSnapshot) (Reference, error) {
	if !scope.valid() || now.IsZero() || !apitypes.ManagementID(snapshot.BridgeID) ||
		!apitypes.ManagementID(snapshot.InstanceGeneration) || snapshot.ObservedAt.IsZero() ||
		now.Before(snapshot.ObservedAt) || now.Sub(snapshot.ObservedAt) > TTL ||
		len(snapshot.Rows) > ovsdb.MaxOpenFlowRows || snapshot.Source != "openflow-1.3-unix" {
		return Reference{}, apitypes.Fail(503, "OPENFLOW_SNAPSHOT_UNPROVEN")
	}
	if snapshot.Rows == nil {
		snapshot.Rows = []ovsdb.OpenFlowRow{}
	}
	data, err := json.Marshal(snapshot)
	for err == nil && len(data) > MaxSnapshotBytes && len(snapshot.Rows) != 0 {
		snapshot.Rows = snapshot.Rows[:len(snapshot.Rows)/2]
		snapshot.Truncated = true
		data, err = json.Marshal(snapshot)
	}
	if err != nil || len(data) > MaxSnapshotBytes {
		return Reference{}, apitypes.Fail(429, "OPENFLOW_SNAPSHOT_BYTE_LIMIT")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := 0
	for id, item := range s.entries {
		if !now.Before(item.ref.ExpiresAt) {
			delete(s.entries, id)
		} else if item.scope.Principal == scope.Principal {
			owned++
		}
	}
	if owned >= maxOwnerSnapshots || len(s.entries) >= maxSnapshots {
		return Reference{}, apitypes.Fail(429, "OPENFLOW_SNAPSHOT_CAPACITY")
	}
	ref := Reference{ID: repository.NewID(), BridgeID: snapshot.BridgeID, Generation: snapshot.InstanceGeneration,
		ObservedAt: snapshot.ObservedAt, ExpiresAt: snapshot.ObservedAt.Add(TTL), Rows: len(snapshot.Rows), Truncated: snapshot.Truncated}
	s.entries[ref.ID] = record{scope: scope, ref: ref, data: data}
	return ref, nil
}

// Read uses the original caller scope and snapshot lifetime. The caller also
// reauthorizes current Bridge access and checks its current generation before
// it returns evidence over the public API.
func (s *Snapshots) Read(now time.Time, scope Scope, id string) (Reference, ovsdb.OpenFlowSnapshot, error) {
	if !scope.valid() || !apitypes.ManagementID(id) {
		return Reference{}, ovsdb.OpenFlowSnapshot{}, apitypes.Fail(404, "OPENFLOW_SNAPSHOT_UNAVAILABLE")
	}
	s.mu.Lock()
	item, ok := s.entries[id]
	if ok && !now.Before(item.ref.ExpiresAt) {
		delete(s.entries, id)
		ok = false
	}
	s.mu.Unlock()
	if !ok || scope != item.scope || now.Before(item.ref.ObservedAt) {
		return Reference{}, ovsdb.OpenFlowSnapshot{}, apitypes.Fail(404, "OPENFLOW_SNAPSHOT_UNAVAILABLE")
	}
	var snapshot ovsdb.OpenFlowSnapshot
	if json.Unmarshal(item.data, &snapshot) != nil || snapshot.BridgeID != item.ref.BridgeID || snapshot.InstanceGeneration != item.ref.Generation {
		return Reference{}, ovsdb.OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_SNAPSHOT_UNPROVEN")
	}
	return item.ref, snapshot, nil
}
