package web

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/publicapi"
	"github.com/sampsonlor/ovs-webui/internal/repository/certificates"
	"github.com/sampsonlor/ovs-webui/internal/repository/requests"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

type certificateContext struct{}
type connectionCertificate struct{ ID atomic.Value }
type servingCertificate struct {
	ID, FallbackID string
	Pair, Fallback *tls.Certificate
	State          tlscontrol.State
}
type ManagedTLS struct {
	store   *certificates.Store
	manager tlscontrol.Manager
	serving atomic.Pointer[servingCertificate]
	mu      sync.Mutex
	local   certificates.LocalState
}

func NewManagedTLS(ctx context.Context, store *certificates.Store, manager tlscontrol.Manager) (*ManagedTLS, error) {
	state, err := store.State(ctx)
	if err != nil {
		return nil, err
	}
	m := &ManagedTLS{store: store, manager: manager, local: state}
	selected, err := m.selectServing(ctx, state.Authority)
	if err != nil {
		return nil, err
	}
	m.serving.Store(selected)
	if state.Authority.TrialID != "" && selected.ID != state.Authority.TrialID && !state.Authority.Expired(tlscontrol.Now()) {
		m.local.Authority = tlscontrol.State{}
	}
	_ = m.Sync(ctx)
	return m, nil
}
func (m *ManagedTLS) selectServing(ctx context.Context, state tlscontrol.State) (*servingCertificate, error) {
	active := state.ActiveID
	if active == "" {
		active = m.local.BootstrapID
	}
	old, _, err := m.store.Load(ctx, active)
	if err != nil {
		return nil, err
	}
	selected := &servingCertificate{ID: active, FallbackID: active, Pair: &old, Fallback: &old, State: state}
	if state.TrialID != "" && !state.Expired(tlscontrol.Now()) {
		pair, _, err := m.store.Load(ctx, state.TrialID)
		if err == nil {
			selected.ID = state.TrialID
			selected.Pair = &pair
		}
	}
	return selected, nil
}
func (m *ManagedTLS) Sync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.manager.TLSState(ctx)
	if err != nil {
		return err
	}
	if state == m.local.Authority {
		return nil
	}
	// Load and validate both identities before publishing a lease. The old
	// in-memory certificate remains available if storage or key access fails.
	selected, err := m.selectServing(ctx, state)
	if err != nil {
		return err
	}
	// If a candidate could not be loaded, retain the old state and retry until
	// the fixed lease ends; a bad candidate never prevents serving the fallback.
	if state.TrialID != "" && !state.Expired(tlscontrol.Now()) && selected.ID != state.TrialID {
		return apitypes.Fail(503, "TLS_CANDIDATE_UNAVAILABLE")
	}
	local := m.local
	local.Authority = state
	if err = m.store.SaveState(ctx, local); err != nil {
		return err
	}
	m.local = local
	m.serving.Store(selected)
	return nil
}
func (m *ManagedTLS) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.Sync(ctx)
		}
	}
}
func (m *ManagedTLS) ConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, certificateContext{}, &connectionCertificate{})
}
func (m *ManagedTLS) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	current := m.serving.Load()
	if current == nil {
		return nil, apitypes.Fail(503, "TLS_IDENTITY_UNAVAILABLE")
	}
	id, pair := current.ID, current.Pair
	// The local consumer enforces the same boot/monotonic recovery deadline even
	// while mgrd or storage is unavailable. No polling success can extend it.
	if current.State.Expired(tlscontrol.Now()) {
		id, pair = current.FallbackID, current.Fallback
	}
	if connection, ok := hello.Context().Value(certificateContext{}).(*connectionCertificate); ok {
		connection.ID.Store(id)
	}
	return pair, nil
}
func servedCertificate(ctx context.Context) string {
	if c, ok := ctx.Value(certificateContext{}).(*connectionCertificate); ok {
		if id, ok := c.ID.Load().(string); ok {
			return id
		}
	}
	return ""
}
func (m *ManagedTLS) Execute(ctx context.Context, s publicapi.Subject, q publicapi.Query, c requests.Command) (apitypes.Result, error) {
	var result apitypes.Result
	input := tlscontrol.Command{Method: c.Method, URI: c.URI, Epoch: c.Epoch, RequestID: c.ID, Precondition: c.Precondition}
	switch q.Operation.ID {
	case "createCertificate":
		d, err := m.store.Stage(ctx, s.ID, c.Epoch, c.ID, c.Payload)
		if err != nil {
			return result, err
		}
		input.Candidate = &d
	case "activateCertificate":
		if _, _, err := m.store.Load(ctx, q.Path["certificate_id"]); err != nil {
			return result, err
		}
	case "confirmCertificate":
		if s.TLSIdentity == "" || s.TLSIdentity != q.Path["certificate_id"] {
			return result, apitypes.Fail(409, "TLS_FRESH_CONNECTION_REQUIRED")
		}
		input.ServedID = s.TLSIdentity
	default:
		return result, apitypes.Fail(503, "TLS_OPERATION_UNAVAILABLE")
	}
	result, err := m.manager.ExecuteTLS(ctx, s.Credential, input)
	if err != nil {
		return result, authError(err)
	}
	// The durable mgrd receipt is returned even if local installation fails; its
	// nonterminal Job and deadline accurately expose pending/rollback recovery.
	_ = m.Sync(ctx)
	return result, nil
}
