package publicapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func streamFixture(t *testing.T) (*Handler, *testAuthority, *httptest.Server) {
	t.Helper()
	a := &testAuthority{}
	h := newTestHandler(t, Options{Authorizer: a})
	s := httptest.NewTLSServer(h)
	h.origin = s.URL
	t.Cleanup(s.Close)
	return h, a, s
}
func dialStream(t *testing.T, s *httptest.Server, origin string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	c, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(s.URL, "https")+"/api/v1/stream", &websocket.DialOptions{HTTPClient: s.Client(), Subprotocols: []string{"ovs.v1"}, HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}
func subscribe(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, _ := json.Marshal(subscription{Resources: []apitypes.Ref{{Kind: "port", ID: resource}}})
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
}
func readEnvelope(t *testing.T, h *Handler, c *websocket.Conn) envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, body, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.contract.Schemas["StreamEnvelope"].Validate(jsonValue(body)); err != nil {
		t.Fatal(err, string(body))
	}
	var e envelope
	if err = json.Unmarshal(body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestWebSocketOriginNotificationsAndRevocation(t *testing.T) {
	h, a, s := streamFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, res, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(s.URL, "https")+"/api/v1/stream", &websocket.DialOptions{HTTPClient: s.Client(), Subprotocols: []string{"ovs.v1"}, HTTPHeader: http.Header{"Origin": []string{"https://hostile.example"}}})
	if err == nil || res == nil || res.StatusCode != 403 {
		t.Fatal("cross origin accepted", err)
	}
	c := dialStream(t, s, s.URL)
	subscribe(t, c)
	first := readEnvelope(t, h, c)
	if first.Type != "resync.required" || first.Sequence != "1" {
		t.Fatal(first)
	}
	if len(h.read) != 0 {
		t.Fatal("websocket retained REST admission slot")
	}
	h.Hub().Publish(Hint{Resource: apitypes.Ref{Kind: "port", ID: resource}, Version: "r2"})
	next := readEnvelope(t, h, c)
	if next.StreamID != first.StreamID || next.Sequence != "2" || next.Type != "resource.changed" {
		t.Fatal(next)
	}
	a.revoked.Store(true)
	h.Hub().Publish(Hint{Resource: apitypes.Ref{Kind: "port", ID: resource}, Version: "r3"})
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("revoked subscriber not closed", err)
	}
}
func TestWebSocketRejectsRPCAndShutdownDrains(t *testing.T) {
	h, _, s := streamFixture(t)
	c := dialStream(t, s, "")
	subscribe(t, c)
	readEnvelope(t, h, c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"method":"Apply"}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal(err)
	}
	d := dialStream(t, s, "")
	subscribe(t, d)
	readEnvelope(t, h, d)
	h.Close()
	if _, _, err = d.Read(ctx); err == nil {
		t.Fatal("shutdown retained socket")
	}
}
func TestWebSocketCounterCoalescing(t *testing.T) {
	h, _, s := streamFixture(t)
	c := dialStream(t, s, "")
	subscribe(t, c)
	readEnvelope(t, h, c)
	for _, revision := range []string{"r1", "r2", "r3"} {
		h.Hub().Publish(Hint{Resource: apitypes.Ref{Kind: "port", ID: resource}, Version: revision, Counter: true})
	}
	e := readEnvelope(t, h, c)
	if e.Type != "state.coalesced" || *e.Version != "r3" || e.Sequence != "2" {
		t.Fatal(e)
	}
}
func TestHubBoundedQueuesAndOverflowRequiresResync(t *testing.T) {
	h := newHub(context.Background())
	defer h.Close()
	ref := apitypes.Ref{Kind: "port", ID: resource}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	s := &subscriber{subject: Subject{ID: principal}, refs: map[apitypes.Ref]bool{ref: true}, wake: make(chan struct{}, 1), cancel: cancel}
	if !h.add(s) {
		t.Fatal("admission failed")
	}
	for i := 0; i < 257; i++ {
		h.Publish(Hint{Resource: ref, Version: "r1"})
	}
	if _, ok, overflow := h.next(s); ok || !overflow || len(s.queue) != 0 || s.bytes != 0 {
		t.Fatal("queue exceeded budget without resync")
	}
	h.remove(s)
	h.maxBytes = 1
	s.overflow = false
	if !h.add(s) {
		t.Fatal("admission failed")
	}
	h.Publish(Hint{Resource: ref, Version: "r1"})
	if _, _, overflow := h.next(s); !overflow {
		t.Fatal("byte budget ignored")
	}
	for i := 0; i < 3; i++ {
		if !h.add(&subscriber{subject: Subject{ID: principal}, cancel: cancel}) {
			t.Fatal("early quota")
		}
	}
	if h.add(&subscriber{subject: Subject{ID: principal}, cancel: cancel}) {
		t.Fatal("principal connection quota exceeded")
	}
}
func TestWebSocketOverflowSendsResyncAndCloses(t *testing.T) {
	h, _, s := streamFixture(t)
	h.hub.maxBytes = 1
	c := dialStream(t, s, "")
	subscribe(t, c)
	readEnvelope(t, h, c)
	h.Hub().Publish(Hint{Resource: apitypes.Ref{Kind: "port", ID: resource}, Version: "r1"})
	if e := readEnvelope(t, h, c); e.Type != "resync.required" || e.Sequence != "2" {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
		t.Fatal(err)
	}
}
