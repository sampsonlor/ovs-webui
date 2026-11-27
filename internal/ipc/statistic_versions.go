package ipc

import (
	"context"
	"net/http"
	"regexp"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const MaxStatisticVersionIDs = 512
const statisticVersionsOperation = "statistics.versions"
const statisticVersionsPath = "/ipc/v1/operations/" + statisticVersionsOperation
const maxStatisticVersionRequestBytes = MaxStatisticVersionIDs*39 + 64 // UUID, two quotes and comma per ID.

var statisticVersion = regexp.MustCompile("^[A-Za-z0-9_-]{1,64}$")

type statisticVersionsRequest struct {
	IDs []string `json:"ids"`
}

type statisticVersionsReply struct {
	Versions map[string]string `json:"versions"`
}

// WithStatisticVersions installs a view of manager-owned cached markers. The
// private transport exposes no counters, discovery, or user-facing operation.
func (h *Handler) WithStatisticVersions(read func([]string) map[string]string) *Handler {
	h.statisticVersions = read
	return h
}

func (h *Handler) statisticVersionsOperation(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) != 0 {
		h.problem(w, r, 400, "AMBIGUOUS_AUTHENTICATION")
		return
	}
	release, err := h.budgets[Observation].acquire(r.Context())
	if err != nil {
		h.queueError(w, r, err)
		return
	}
	defer release()
	r.Body = http.MaxBytesReader(w, r.Body, maxStatisticVersionRequestBytes)
	var input statisticVersionsRequest
	if !h.decode(w, r, &input) {
		return
	}
	ids := statisticVersionIDs(input.IDs)
	if ids == nil {
		h.problem(w, r, 400, "IPC_INVALID_REQUEST")
		return
	}
	if h.statisticVersions == nil {
		h.problem(w, r, 503, "STATISTIC_VERSIONS_UNAVAILABLE")
		return
	}
	versions := h.statisticVersions(input.IDs)
	if r.Context().Err() != nil {
		h.problem(w, r, 503, "REQUEST_INTERRUPTED")
		return
	}
	if !validStatisticVersions(ids, versions) {
		h.problem(w, r, 503, "STATISTIC_VERSIONS_UNAVAILABLE")
		return
	}
	h.write(w, 200, statisticVersionsReply{Versions: versions})
}

func statisticVersionIDs(ids []string) map[string]bool {
	if len(ids) == 0 || len(ids) > MaxStatisticVersionIDs {
		return nil
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !apitypes.ManagementID(id) || set[id] {
			return nil
		}
		set[id] = true
	}
	return set
}

func validStatisticVersions(ids map[string]bool, versions map[string]string) bool {
	if versions == nil || len(versions) > len(ids) {
		return false
	}
	for id, version := range versions {
		if !ids[id] || !statisticVersion.MatchString(version) {
			return false
		}
	}
	return true
}

func (c *Client) StatisticVersions(ctx context.Context, ids []string) (map[string]string, error) {
	requested := statisticVersionIDs(ids)
	if requested == nil {
		return nil, &RemoteError{Code: "IPC_INVALID_REQUEST", Status: 400}
	}
	var reply statisticVersionsReply
	if err := c.callAuth(ctx, statisticVersionsOperation, "", statisticVersionsRequest{IDs: ids}, &reply); err != nil {
		return nil, err
	}
	if !validStatisticVersions(requested, reply.Versions) {
		return nil, &RemoteError{Code: "IPC_INVALID_RESPONSE", Status: 503}
	}
	return reply.Versions, nil
}
