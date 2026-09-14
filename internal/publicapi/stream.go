package publicapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type Hint struct {
	Resource               apitypes.Ref
	Version, CorrelationID string
	Counter                bool
}
type envelope struct {
	StreamID    string        `json:"stream_id"`
	Sequence    string        `json:"sequence"`
	Type        string        `json:"type"`
	Resource    *apitypes.Ref `json:"resource_ref"`
	Version     *string       `json:"resource_version"`
	ObservedAt  time.Time     `json:"observed_at"`
	Correlation *string       `json:"correlation_id"`
}
type subscription struct {
	Resources []apitypes.Ref `json:"resources"`
	Cursor    *struct {
		StreamID string `json:"stream_id"`
		Sequence string `json:"sequence"`
	} `json:"cursor"`
}
type queuedHint struct {
	hint  Hint
	size  int
	ready time.Time
}
type subscriber struct {
	subject  Subject
	refs     map[apitypes.Ref]bool
	queue    []queuedHint
	bytes    int
	overflow bool
	wake     chan struct{}
	cancel   context.CancelFunc
}
type Hub struct {
	mu                   sync.Mutex
	clients              map[*subscriber]bool
	ctx                  context.Context
	cancel               context.CancelFunc
	closed               bool
	maxEntries, maxBytes int
}

func newHub(ctx context.Context) *Hub {
	ctx, cancel := context.WithCancel(ctx)
	h := &Hub{clients: map[*subscriber]bool{}, ctx: ctx, cancel: cancel, maxEntries: 256, maxBytes: 1 << 20}
	go func() { <-ctx.Done(); h.Close() }()
	return h
}
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.cancel()
	for client := range h.clients {
		client.cancel()
	}
}
func (h *Hub) add(s *subscriber) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.clients) >= 16 {
		return false
	}
	n := 0
	for c := range h.clients {
		if c.subject.ID == s.subject.ID {
			n++
		}
	}
	if n >= 4 {
		return false
	}
	h.clients[s] = true
	return true
}
func (h *Hub) remove(s *subscriber) { h.mu.Lock(); defer h.mu.Unlock(); delete(h.clients, s) }

// Publish carries invalidation hints only, never authoritative Job/Audit data.
// Counters for the same resource share one queue entry and a one-second window.
func (h *Hub) Publish(v Hint) {
	if !apitypes.ManagementID(v.Resource.ID) || streamCapabilities[v.Resource.Kind] == "" || !strongETag.MatchString(`"`+v.Version+`"`) || (v.CorrelationID != "" && !apitypes.ManagementID(v.CorrelationID)) {
		return
	}
	encoded, _ := json.Marshal(v)
	size := len(encoded)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for s := range h.clients {
		if !s.refs[v.Resource] || s.overflow {
			continue
		}
		replaced := false
		if v.Counter {
			for i, item := range s.queue {
				if item.hint.Counter && item.hint.Resource == v.Resource {
					s.bytes += size - item.size
					s.queue[i].hint = v
					s.queue[i].size = size
					replaced = true
					break
				}
			}
		}
		if !replaced {
			ready := time.Now()
			if v.Counter {
				ready = ready.Add(time.Second)
			}
			s.queue = append(s.queue, queuedHint{hint: v, size: size, ready: ready})
			s.bytes += size
		}
		if len(s.queue) > h.maxEntries || s.bytes > h.maxBytes {
			s.queue = nil
			s.bytes = 0
			s.overflow = true
		}
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}
func (h *Hub) next(s *subscriber) (Hint, bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.overflow {
		return Hint{}, false, true
	}
	if len(s.queue) == 0 || s.queue[0].ready.After(time.Now()) {
		return Hint{}, false, false
	}
	v := s.queue[0]
	s.queue = s.queue[1:]
	s.bytes -= v.size
	return v.hint, true, false
}

var streamCapabilities = map[string]string{
	"bridge": "state.read", "port": "state.read", "interface": "state.read", "bond": "state.read", "health": "state.read", "capability": "state.read", "statistic": "state.read",
	"candidate": "workspace.read", "validation": "config.validate", "transaction": "config.read", "job": "job.read", "event": "event.read", "audit": "audit.read",
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request, subject Subject) {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"ovs.v1"}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	if conn.Subprotocol() != "ovs.v1" {
		_ = conn.Close(websocket.StatusPolicyViolation, "PROTOCOL_REQUIRED")
		return
	}
	conn.SetReadLimit(8192)
	ctx, cancel := context.WithCancel(h.hub.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.CloseNow() })
	defer stop()
	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	kind, data, err := conn.Read(readCtx)
	readCancel()
	if err != nil {
		return
	}
	var sub subscription
	if kind != websocket.MessageText || ipc.DecodeStrict(data, &sub) != nil || len(sub.Resources) > 32 || len(sub.Resources) == 0 {
		_ = conn.Close(websocket.StatusPolicyViolation, "INVALID_SUBSCRIPTION")
		return
	}
	if h.contract.Schemas["Subscription"].Validate(jsonValue(data)) != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "INVALID_SUBSCRIPTION")
		return
	}
	s := &subscriber{subject: subject, refs: map[apitypes.Ref]bool{}, wake: make(chan struct{}, 1), cancel: cancel}
	check := func(refs []apitypes.Ref) error {
		authCtx, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		current, e := h.authorizer.Revalidate(authCtx, subject)
		if e != nil {
			return e
		}
		if current.ID != subject.ID || current.PermissionRevision != subject.PermissionRevision {
			return apitypes.Fail(403, "AUTHORIZATION_CHANGED")
		}
		for _, ref := range refs {
			capability := streamCapabilities[ref.Kind]
			if capability == "" || !apitypes.ManagementID(ref.ID) {
				return apitypes.Fail(403, "FORBIDDEN")
			}
			if e = h.authorizer.Allow(authCtx, current, capability, []apitypes.Ref{ref}); e != nil {
				return e
			}
		}
		return nil
	}
	if check(sub.Resources) != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "AUTHORIZATION_CHANGED")
		return
	}
	for _, ref := range sub.Resources {
		s.refs[ref] = true
	}
	if !h.hub.add(s) {
		_ = conn.Close(websocket.StatusTryAgainLater, "RESOURCE_BUDGET_EXCEEDED")
		return
	}
	defer h.hub.remove(s)
	// After subscription, every application frame is rejected. Ping/pong control
	// frames are handled by the library; this socket cannot become an RPC tunnel.
	go func() {
		defer cancel()
		_, _, e := conn.Read(ctx)
		if e == nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "NOTIFICATIONS_ONLY")
		}
	}()
	streamID := repository.NewID()
	var sequence uint64
	send := func(t string, hint *Hint) bool {
		refs := sub.Resources
		if hint != nil {
			refs = []apitypes.Ref{hint.Resource}
		}
		if check(refs) != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "AUTHORIZATION_CHANGED")
			return false
		}
		if sequence == math.MaxUint64 {
			return false
		}
		sequence++
		e := envelope{StreamID: streamID, Sequence: strconv.FormatUint(sequence, 10), Type: t, ObservedAt: time.Now().UTC()}
		if hint != nil {
			e.Resource = &hint.Resource
			e.Version = &hint.Version
			if hint.CorrelationID != "" {
				e.Correlation = &hint.CorrelationID
			}
		}
		body, _ := json.Marshal(e)
		writeCtx, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		return conn.Write(writeCtx, websocket.MessageText, body) == nil
	}
	// A new connection/stream cannot certify continuity with the previous one.
	if !send("resync.required", nil) {
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if !send("heartbeat", nil) {
				return
			}
		case <-s.wake:
		case <-tick.C:
		}
		for {
			hint, ok, overflow := h.hub.next(s)
			if overflow {
				_ = send("resync.required", nil)
				_ = conn.Close(websocket.StatusTryAgainLater, "RESYNC_REQUIRED")
				return
			}
			if !ok {
				break
			}
			kind := "resource.changed"
			if hint.Counter {
				kind = "state.coalesced"
			}
			if !send(kind, &hint) {
				return
			}
		}
	}
}
