//go:build linux

package ipc

import (
	"errors"
	"os"
	"testing"
)

func TestIPCStatisticVersionsNegotiatedUnixRoundTrip(t *testing.T) {
	handler := NewHandler(CurrentProtocol("test-build"), nil, nil).WithStatisticVersions(func(ids []string) map[string]string {
		return map[string]string{ids[0]: "sample-1"}
	})
	client, _ := testServer(t, handler, uint32(os.Geteuid()))
	scope := []string{statisticTestID, unrelatedStatisticID}
	if reply, err := client.StatisticVersions(t.Context(), scope); err != nil || len(reply) != 1 || reply[statisticTestID] != "sample-1" {
		t.Fatalf("invalid reply or handshake failure: %v %v", reply, err)
	}
	other := *client
	other.protocol.Minor--
	var remote *RemoteError
	if _, err := other.StatisticVersions(t.Context(), scope); !errors.As(err, &remote) || remote.Code != "IPC_VERSION_MISMATCH" {
		t.Fatalf("accepted old protocol: %v", err)
	}
	badPeer, _ := testServer(t, handler, uint32(os.Geteuid())+1)
	if _, err := badPeer.StatisticVersions(t.Context(), scope); err == nil {
		t.Fatal("accepted wrong Unix peer")
	}
}
