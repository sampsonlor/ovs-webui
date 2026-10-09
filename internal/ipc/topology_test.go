package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestStrictTopologyOriginalsRoundTripNativeSummariesAndSeal(t *testing.T) {
	port := candidate.TopologyNode{Binding: candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Port", Generation: repository.NewID()}, Name: "synthetic-port", Links: []candidate.Binding{}, Digest: candidate.Digest("port")}
	port.Summary = candidate.TopologySummary("Port", map[string]any{"vlan_mode": []any{}, "tag": []any{"23"}, "trunks": []any{}, "cvlans": []any{}, "lacp": []any{"off"}, "bond_mode": []any{}, "other_config": map[string]any{"lacp-fallback-ab": "false", "private-key": "must-not-leak"}})
	iface := candidate.TopologyNode{Binding: candidate.Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: "Interface", Generation: port.Binding.Generation}, Name: "synthetic-if", Type: "internal", Links: []candidate.Binding{}, Digest: candidate.Digest("interface")}
	iface.Summary = candidate.TopologySummary("Interface", map[string]any{"ofport_request": []any{}, "mtu_request": []any{"1500"}, "ingress_policing_rate": "0", "ingress_policing_burst": "0", "ingress_policing_kpkts_rate": "0", "ingress_policing_kpkts_burst": "0"})
	port.Links = []candidate.Binding{iface.Binding}
	e := candidate.Envelope{Owner: "synthetic-owner", Epoch: repository.NewID(), Sequence: 1, Candidate: candidate.Candidate{ID: repository.NewID(), Revision: repository.NewID(), Intents: []candidate.StoredIntent{{ID: repository.NewID(), Operation: "port.move", Object: port.Binding, Topology: &candidate.TopologyChange{Before: []candidate.TopologyNode{port, iface}, After: []candidate.TopologyNode{port, iface}, Restored: []candidate.TopologyNode{}, Replacements: map[string]candidate.Binding{}}}}}}
	key := bytes.Repeat([]byte{7}, 32)
	e.Sign(key)
	for _, tc := range []struct {
		name   string
		input  any
		output any
	}{
		{"read", candidate.ReadRequest{Envelope: e}, &candidate.ReadRequest{}},
		{"validate", candidate.ValidateRequest{Envelope: e}, &candidate.ValidateRequest{}},
		{"prepare", candidate.PrepareRequest{Envelope: e}, &candidate.PrepareRequest{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.input)
			if err != nil || strings.Contains(string(body), "must-not-leak") || !bytes.Contains(body, []byte(`"trunks":[]`)) || !bytes.Contains(body, []byte(`"ingress_policing_rate":"0"`)) {
				t.Fatal("native absence, zero or privacy changed", err)
			}
			if err = DecodeStrict(body, tc.output); err != nil {
				t.Fatal("nonempty topology envelope rejected", err)
			}
			canonical, err := json.Marshal(tc.output)
			if err != nil || !bytes.Equal(body, canonical) {
				t.Fatal("signed topology originals changed on IPC round trip")
			}
			for _, invalid := range []string{
				strings.Replace(string(body), `"configuration_summary":{`, `"configuration_summary":{"private_configuration":{},`, 1),
				strings.Replace(string(body), `"trunks":[]`, `"Trunks":[]`, 1),
				strings.Replace(string(body), `"trunks":[]`, `"trunks":[],"trunks":["23"]`, 1),
			} {
				if DecodeStrict([]byte(invalid), tc.output) == nil {
					t.Fatal("unknown, case-ambiguous or duplicate summary field accepted")
				}
			}
		})
	}
	body, _ := json.Marshal(candidate.ValidateRequest{Envelope: e})
	var out candidate.ValidateRequest
	if err := DecodeStrict(body, &out); err != nil || out.Envelope.Verify(key, e.Owner) != nil {
		t.Fatal("topology seal did not survive IPC", err)
	}
	forged := strings.Replace(string(body), `"tag":["23"]`, `"tag":["24"]`, 1)
	if err := DecodeStrict([]byte(forged), &out); err != nil || out.Envelope.Verify(key, e.Owner) == nil {
		t.Fatal("modified summary retained a valid seal", err)
	}
}
