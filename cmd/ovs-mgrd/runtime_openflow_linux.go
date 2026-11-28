//go:build linux

package main

import (
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func parseOpenFlowSockets(input string) (map[string]string, error) {
	bindings := map[string]string{}
	if input == "" {
		return bindings, nil
	}
	if len(input) > 18<<10 {
		return nil, apitypes.Fail(422, "INVALID_OPENFLOW_ENDPOINT_SCOPE")
	}
	for _, entry := range strings.Split(input, ",") {
		id, socket, ok := strings.Cut(entry, "=")
		if !ok || !apitypes.ManagementID(id) || bindings[id] != "" || len(bindings) == 128 ||
			socket == "" || len(socket) > 100 || strings.ContainsAny(socket, "\x00\r\n=") {
			return nil, apitypes.Fail(422, "INVALID_OPENFLOW_ENDPOINT_SCOPE")
		}
		bindings[id] = socket
	}
	return bindings, nil
}
