package certificates

import (
	"context"
	"encoding/pem"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
)

// Rewrap runs only while the daemon is stopped. The prepared key is durable and
// LoadKeys can read it even if power fails before the active pointer is switched.
func (s *Store) Rewrap(ctx context.Context, next secret.Ring) error {
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	if err = s.Secrets.Rewrap(ctx, next); err != nil {
		return err
	}
	s.Secrets.Ring = next
	return s.SaveState(ctx, state)
}
func (s *Store) PrepareRestore(ctx context.Context) error {
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	active := state.Authority.ActiveID
	if active == "" {
		active = state.BootstrapID
	}
	if _, _, err = s.Load(ctx, active); err != nil {
		return err
	}
	state.Authority = tlscontrol.State{ActiveID: state.Authority.ActiveID}
	return s.SaveState(ctx, state)
}

// PublicCertificate intentionally returns only DER certificate blocks.
func (s *Store) PublicCertificate(ctx context.Context) ([]byte, error) {
	state, err := s.State(ctx)
	if err != nil {
		return nil, err
	}
	id := state.Authority.ActiveID
	if id == "" {
		id = state.BootstrapID
	}
	pair, _, err := s.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, der := range pair.Certificate {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out, nil
}
