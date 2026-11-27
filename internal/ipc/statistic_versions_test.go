package ipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const statisticTestID = "00000000-0000-4000-8000-000000000033"
const unrelatedStatisticID = "00000000-0000-4000-8000-000000000044"

func TestIPCStatisticVersionsRequiresTrustedNegotiatedPeer(t *testing.T) {
	negotiated := &connectionState{}
	negotiated.negotiated.Store(true)
	var calls int
	handler := NewHandler(CurrentProtocol("test"), nil, nil).WithStatisticVersions(func(ids []string) map[string]string {
		calls++
		if len(ids) != 1 || ids[0] != statisticTestID {
			t.Fatalf("invalid IDs reached the cache: %v", ids)
		}
		return map[string]string{statisticTestID: "sample-1"}
	})
	for _, scenario := range []struct {
		name   string
		state  *connectionState
		grant  string
		body   string
		status int
	}{
		{"no peer", nil, "", `{"ids":["` + statisticTestID + `"]}`, 403},
		{"no handshake", &connectionState{}, "", `{"ids":["` + statisticTestID + `"]}`, 409},
		{"grant is not a private peer authority", negotiated, "Bearer fake", `{"ids":["` + statisticTestID + `"]}`, 400},
		{"unknown input", negotiated, "", `{"ids":["` + statisticTestID + `"],"all":true}`, 400},
		{"duplicates", negotiated, "", `{"ids":["` + statisticTestID + `","` + statisticTestID + `"]}`, 400},
		{"empty scope", negotiated, "", `{"ids":[]}`, 400},
		{"accepted scope", negotiated, "", `{"ids":["` + statisticTestID + `"]}`, 200},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, statisticVersionsPath, strings.NewReader(scenario.body))
			request.Header.Set("Content-Type", "application/json")
			if scenario.grant != "" {
				request.Header.Set("Authorization", scenario.grant)
			}
			if scenario.state != nil {
				request = request.WithContext(context.WithValue(request.Context(), connectionKey{}, scenario.state))
			}
			reply := httptest.NewRecorder()
			handler.ServeHTTP(reply, request)
			if reply.Code != scenario.status {
				t.Fatalf("got %d, want %d, reply: %s", reply.Code, scenario.status, reply.Body.String())
			}
			if scenario.status == 200 {
				var data statisticVersionsReply
				if json.Unmarshal(reply.Body.Bytes(), &data) != nil || len(data.Versions) != 1 || data.Versions[statisticTestID] != "sample-1" {
					t.Fatal("invalid scoped reply", reply.Body.String())
				}
			}
		})
	}
	if calls != 1 {
		t.Fatalf("called manager cache %d times instead of once", calls)
	}
}

func TestIPCStatisticVersionsRejectsUnscopedAndUnavailableResults(t *testing.T) {
	ids := statisticVersionIDs([]string{statisticTestID})
	for _, invalid := range []map[string]string{
		nil,
		{unrelatedStatisticID: "sample-1"},
		{statisticTestID: "bad version"},
		{statisticTestID: strings.Repeat("a", 65)},
	} {
		if validStatisticVersions(ids, invalid) {
			t.Fatal("accepted invalid response", invalid)
		}
	}
	if !validStatisticVersions(ids, map[string]string{}) || !validStatisticVersions(ids, map[string]string{statisticTestID: strings.Repeat("a", 64)}) {
		t.Fatal("rejected scoped response")
	}
	if statisticVersionIDs(make([]string, MaxStatisticVersionIDs+1)) != nil {
		t.Fatal("accepted oversized request")
	}
}
