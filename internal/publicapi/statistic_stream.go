package publicapi

import (
	"context"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
)

const statisticKind = "statistic"

// RunStatisticFeed is the web daemon's single consumer of manager-owned cache
// revisions. Subscription admission and delivery still use the public stream's
// per-resource authorization checks.
func (h *Hub) RunStatisticFeed(read func(context.Context, []string) (map[string]string, error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var previous map[string]string
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-tick.C:
			previous = h.pollStatisticFeed(read, previous)
		}
	}
}

func (h *Hub) pollStatisticFeed(read func(context.Context, []string) (map[string]string, error), previous map[string]string) map[string]string {
	ids := h.statisticFeedIDs()
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > ipc.MaxStatisticVersionIDs {
		h.statisticFeedGap(ids)
		return nil
	}
	ctx, cancel := context.WithTimeout(h.ctx, 700*time.Millisecond)
	defer cancel()
	versions, err := read(ctx, ids)
	if err != nil || versions == nil {
		h.statisticFeedGap(ids)
		return nil
	}
	current := make(map[string]string, len(ids))
	var unavailable []string
	for _, id := range ids {
		version := versions[id]
		if len(version) > 64 || !strongETag.MatchString(`"`+version+`"`) {
			if previous[id] != "" {
				unavailable = append(unavailable, id)
			}
			continue
		}
		current[id] = version
		if version != previous[id] {
			h.Publish(Hint{Resource: apitypes.Ref{Kind: statisticKind, ID: id}, Version: version, Counter: true})
		}
	}
	h.statisticFeedGap(unavailable)
	return current
}

func (h *Hub) statisticFeedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	distinct := make(map[string]bool)
	for client := range h.clients {
		if client.overflow {
			continue
		}
		for ref := range client.refs {
			if ref.Kind == statisticKind {
				distinct[ref.ID] = true
			}
		}
	}
	ids := make([]string, 0, len(distinct))
	for id := range distinct {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (h *Hub) statisticFeedGap(ids []string) {
	if len(ids) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		for _, id := range ids {
			if client.refs[apitypes.Ref{Kind: statisticKind, ID: id}] {
				client.queue, client.bytes, client.overflow = nil, 0, true
				select {
				case client.wake <- struct{}{}:
				default:
				}
				break
			}
		}
	}
}
