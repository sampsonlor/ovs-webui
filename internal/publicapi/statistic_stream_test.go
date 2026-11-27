package publicapi

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func TestStatisticFeedDeduplicatesScopesAndIsolatesUnavailableResources(t *testing.T) {
	h := newHub(t.Context())
	defer h.Close()
	add := func(kind, id string) *subscriber {
		t.Helper()
		s := &subscriber{
			subject: Subject{ID: principal}, refs: map[apitypes.Ref]bool{{Kind: kind, ID: id}: true},
			wake: make(chan struct{}, 1), cancel: func() {},
		}
		if !h.add(s) {
			t.Fatal("subscriber admission failed")
		}
		return s
	}
	other := add("port", resource)
	h.pollStatisticFeed(func(context.Context, []string) (map[string]string, error) {
		t.Fatal("non-statistic subscriber started an observation probe")
		return nil, nil
	}, nil)

	a, duplicate := add(statisticKind, resource), add(statisticKind, resource)
	b := add(statisticKind, epoch)
	versions := map[string]string{resource: "old", epoch: "stable", principal: "unrelated"}
	calls := 0
	read := func(_ context.Context, ids []string) (map[string]string, error) {
		calls++
		if !slices.Equal(ids, []string{epoch, resource}) {
			t.Fatalf("wrong scoped IDs %v", ids)
		}
		return versions, nil
	}
	previous := h.pollStatisticFeed(read, nil)
	versions[resource] = "new"
	previous = h.pollStatisticFeed(read, previous)
	if calls != 2 || len(previous) != 2 || previous[principal] != "" || len(other.queue) != 0 {
		t.Fatal("relay included unrelated data or probed per subscriber")
	}
	for _, subscribed := range []*subscriber{a, duplicate, b} {
		if len(subscribed.queue) != 1 || !subscribed.queue[0].hint.Counter ||
			subscribed.queue[0].hint.Version != versions[subscribed.queue[0].hint.Resource.ID] {
			t.Fatalf("filtered coalescing did not retain the latest scoped version: %v", subscribed.queue)
		}
	}
	delete(versions, resource)
	h.pollStatisticFeed(read, previous)
	if !a.overflow || !duplicate.overflow || len(a.queue) != 0 || b.overflow || other.overflow {
		t.Fatal("source loss did not isolate the affected subscribers")
	}
}

func TestStatisticFeedDeliversAfterInitialResyncAndRevalidates(t *testing.T) {
	h, authority, server := streamFixture(t)
	conn := dialStream(t, server, server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	payload, _ := json.Marshal(subscription{Resources: []apitypes.Ref{{Kind: statisticKind, ID: resource}}})
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	if first := readEnvelope(t, h, conn); first.Type != "resync.required" || first.Sequence != "1" {
		t.Fatal(first)
	}
	read := func(_ context.Context, ids []string) (map[string]string, error) {
		if !slices.Equal(ids, []string{resource}) {
			t.Fatalf("wrong subscribed Statistic %v", ids)
		}
		return map[string]string{resource: "sample-1"}, nil
	}
	h.hub.pollStatisticFeed(read, nil)
	hint := readEnvelope(t, h, conn)
	if hint.Type != "state.coalesced" || hint.Sequence != "2" ||
		hint.Resource == nil || *hint.Resource != (apitypes.Ref{Kind: statisticKind, ID: resource}) ||
		hint.Version == nil || *hint.Version != "sample-1" {
		t.Fatal(hint)
	}
	authority.revoked.Store(true)
	h.hub.pollStatisticFeed(read, nil)
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("revoked statistic subscription was not closed", err)
	}
}
