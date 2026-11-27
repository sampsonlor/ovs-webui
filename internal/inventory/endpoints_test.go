package inventory

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

func TestEndpointEvidenceDoesNotJoinAcrossSourcesOrAttachment(t *testing.T) {
	observedAt := time.Date(2026, 1, 4, 8, 30, 0, 0, time.UTC)
	mac := "02:00:00:24:00:01"
	bridgeID := schemaID("endpoints-test", "bridge")
	portID := schemaID("endpoints-test", "port")
	interfaceID := schemaID("endpoints-test", "interface")
	exactPort, externalPort := "test-port", "outside-port"
	vlan101, vlan202, ofport, age := uint16(101), uint16(202), uint16(9), uint32(5)
	bridge := fdbBridge{binding: Binding{ManagementID: bridgeID}, name: "test-bridge"}
	fdb := []fdbEntry{
		{
			FDBNativeRow: FDBNativeRow{MAC: mac, VLAN: &vlan101, OFPort: &ofport, PortName: &exactPort, AgeSeconds: &age},
			id:           schemaID("endpoints-test", "fdb-101"), bridge: bridge,
			port: Binding{ManagementID: portID}, portResolution: "resolved", observedAt: observedAt,
		},
		{
			FDBNativeRow: FDBNativeRow{MAC: mac, VLAN: &vlan202, OFPort: &ofport, PortName: &externalPort},
			id:           schemaID("endpoints-test", "fdb-202"), bridge: bridge, portResolution: "unmapped", observedAt: observedAt,
		},
	}
	local := neighborBinding{
		deviceProof: deviceProof{managementID: interfaceID, ovsUUID: schemaID("endpoints-test", "native-interface"), request: DeviceRequest{Name: "test-if", IfIndex: 20}},
		port:        neighborParent{portID, exactPort}, bridge: neighborParent{bridgeID, bridge.name},
	}
	other := local
	other.managementID = schemaID("endpoints-test", "other-interface")
	other.ovsUUID = schemaID("endpoints-test", "other-native-interface")
	other.request = DeviceRequest{Name: "test-if-other", IfIndex: 21}
	neighbors := []neighborEntry{
		{ID: schemaID("endpoints-test", "arp"), IP: "192.0.2.1", MAC: mac, State: "reachable", Family: "ipv4", Binding: local},
		{ID: schemaID("endpoints-test", "ndp"), IP: "2001:db8::1", MAC: mac, State: "stale", Family: "ipv6", Binding: local},
		{ID: schemaID("endpoints-test", "other-arp"), IP: "192.0.2.1", MAC: mac, State: "permanent", Family: "ipv4", Binding: other},
		{ID: schemaID("endpoints-test", "failed-arp"), IP: "192.0.2.2", State: "failed", Family: "ipv4", Binding: local},
	}
	peers := []lldpPeer{{
		id: schemaID("endpoints-test", "peer"), binding: local,
		frame: LLDPFrame{
			ChassisID:  LLDPID{Subtype: 4, Encoding: "mac", Value: mac},
			PortID:     LLDPID{Subtype: 5, Encoding: "text", Value: "Gi0/99"},
			SystemName: "SW-Beta", TTL: 120, ReceivedAt: observedAt.Add(-7 * time.Second),
		},
	}}
	nativeNeighborSource := map[string]any{"authority": "linux-rtnetlink-neighbors", "observed_at": observedAt.Add(-2 * time.Second)}
	nativeLLDPSource := map[string]any{"authority": "linux-passive-lldp", "observed_at": observedAt.Add(-4 * time.Second)}
	groups := buildEndpoints(fdb, neighbors, peers, nativeNeighborSource, nativeLLDPSource)
	if len(groups) != 6 || !slices.IsSortedFunc(groups, func(a, b endpointGroup) int { return strings.Compare(a.id, b.id) }) {
		t.Fatalf("expected six scoped endpoint groups in stable order, got %#v", groups)
	}
	for _, group := range groups {
		resource := group.resource(5, maxEndpointDetailEvidence, "")
		payload := resource["endpoint"].(map[string]any)
		evidence := payload["evidence"].([]endpointEvidence)
		if len(evidence) == 0 {
			t.Fatal("endpoint without source evidence")
		}
		switch group.basis {
		case "fdb":
			if group.vlan == nil || group.mac != mac || group.bridge.id != bridgeID || group.iface.id != "" ||
				group.hasAddress("192.0.2.1") || len(payload["addresses"].([]string)) != 0 || evidence[0].Source["observed_at"] != observedAt {
				t.Fatalf("FDB evidence crossed attachment or source domains: %#v", resource)
			}
			if *group.vlan == vlan101 {
				if payload["port_ref"] != (apitypes.Ref{Kind: "port", ID: portID}) ||
					evidence[0].Ref.Kind != "fdb_entry" || evidence[0].LastSeenAt == nil ||
					!evidence[0].LastSeenAt.Equal(observedAt.Add(-5*time.Second)) {
					t.Fatalf("missing exact Port or native age estimate: %#v", resource)
				}
			} else if payload["port_ref"] != nil || evidence[0].PortName == nil || *evidence[0].PortName != externalPort ||
				evidence[0].PortResolution != "unmapped" || evidence[0].LastSeenAt != nil || !group.matchesText("Outside-Port") {
				t.Fatalf("raw FDB target gained a Port reference or lost its evidence: %#v", resource)
			}
		case "neighbor":
			if group.vlan != nil || evidence[0].Ref.Kind != "neighbor" || evidence[0].LastSeenAt != nil ||
				evidence[0].Source["observed_at"] != nativeNeighborSource["observed_at"] {
				t.Fatalf("neighbor evidence inherited VLAN/last seen or lost source: %#v", resource)
			}
			switch {
			case group.iface.id == interfaceID && group.mac == mac:
				if len(evidence) != 2 || !group.hasAddress("192.0.2.1") || !group.hasAddress("2001:db8::1") ||
					group.hasAddress("192.0.2.2") || evidence[1].Family != "ipv6" {
					t.Fatalf("same-interface MAC should group just its confirmed address evidence: %#v", resource)
				}
			case group.iface.id == other.managementID:
				if len(evidence) != 1 || evidence[0].Ref.ID != neighbors[2].ID {
					t.Fatalf("same MAC and IP on another interface joined the first endpoint: %#v", resource)
				}
			default:
				if group.mac != "" || len(evidence) != 1 || evidence[0].State != "failed" || !group.hasAddress("192.0.2.2") {
					t.Fatalf("failed neighbor should retain standalone raw IP/state: %#v", resource)
				}
			}
		case "lldp":
			e := evidence[0]
			if group.mac != "" || group.vlan != nil || group.hasAddress("192.0.2.1") || !group.matchesText("sw-beta") ||
				e.ChassisID == nil || e.ChassisID.Value != mac || e.LLDPPortID == nil || e.LLDPPortID.Value != "Gi0/99" ||
				e.Source["observed_at"] != nativeLLDPSource["observed_at"] || e.TTLSeconds != peers[0].frame.TTL || e.LastSeenAt == nil ||
				!e.LastSeenAt.Equal(peers[0].frame.ReceivedAt) {
				t.Fatalf("advertised LLDP MAC became endpoint MAC or lost its raw IDs: %#v", resource)
			}
		default:
			t.Fatalf("unknown endpoint basis %q", group.basis)
		}
	}
}

func TestEndpointEvidenceCapKeepsExactIPFilterInspectable(t *testing.T) {
	attachment := neighborBinding{deviceProof: deviceProof{
		managementID: schemaID("endpoints-test-preview", "iface"), ovsUUID: schemaID("endpoints-test-preview", "native-iface"),
	}}
	neighbors := make([]neighborEntry, 0, 16)
	for i := 1; i <= cap(neighbors); i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		neighbors = append(neighbors, neighborEntry{
			ID: schemaID("endpoints-test-preview", ip), IP: ip, MAC: "02:00:00:99:00:01", State: "reachable", Family: "ipv4", Binding: attachment,
		})
	}
	groups := buildEndpoints(nil, neighbors, nil, nil, nil)
	ip := neighbors[len(neighbors)-1].IP
	if len(groups) != 1 || !groups[0].hasAddress(ip) || !groups[0].matchesText(ip) {
		t.Fatalf("preferred address was excluded from the complete retained group: %#v", groups)
	}
	for _, limit := range []int{2, maxEndpointDetailEvidence, maxEndpointDetailEvidence + 1} {
		detail := groups[0].resource(1, limit, ip)["endpoint"].(map[string]any)
		evidence := detail["evidence"].([]endpointEvidence)
		addresses := detail["addresses"].([]string)
		if len(evidence) != min(limit, maxEndpointDetailEvidence) || evidence[0].IP != ip ||
			len(addresses) != len(evidence) || addresses[0] != ip ||
			detail["evidence_total"] != len(neighbors) || detail["evidence_truncated"] != true {
			t.Fatalf("capped preview %d omitted priority evidence or coverage: %#v", limit, detail)
		}
	}
	if first := groups[0].evidence[0].IP; first != neighbors[0].IP {
		t.Fatalf("building a preferred preview mutated the full group: got %s", first)
	}
	if remaining := buildEndpoints(nil, neighbors[1:], nil, nil, nil); len(remaining) != 1 || remaining[0].id != groups[0].id {
		t.Fatal("a row change should preserve the same-interface MAC group identity")
	}
}
