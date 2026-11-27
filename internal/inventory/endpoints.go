package inventory

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const maxEndpointDetailEvidence = 12

type endpointEvidence struct {
	Ref            apitypes.Ref   `json:"ref"`
	Source         map[string]any `json:"source"`
	State          string         `json:"state"`
	IP             string         `json:"ip,omitempty"`
	Family         string         `json:"family,omitempty"`
	MAC            string         `json:"mac,omitempty"`
	VLAN           *uint16        `json:"vlan,omitempty"`
	OFPort         *uint16        `json:"ofport,omitempty"`
	PortName       *string        `json:"port_name,omitempty"`
	PortResolution string         `json:"port_resolution,omitempty"`
	ChassisID      *LLDPID        `json:"chassis_id,omitempty"`
	LLDPPortID     *LLDPID        `json:"lldp_port_id,omitempty"`
	SystemName     string         `json:"system_name,omitempty"`
	TTLSeconds     uint16         `json:"ttl_seconds,omitempty"`
	LastSeenAt     *time.Time     `json:"last_seen_at,omitempty"`
}

type endpointGroup struct {
	id, basis, mac      string
	vlan                *uint16
	bridge, port, iface neighborParent
	evidence            []endpointEvidence
}

// The caller selects current, independently qualified observations and
// excludes expired LLDP peers. A group is an observed identity within one
// source domain. None of these keys asserts a remote device identity, and a
// matching MAC never supplies a missing VLAN or joins different domains.
func buildEndpoints(fdb []fdbEntry, neighbors []neighborEntry, peers []lldpPeer, neighborSource, lldpSource map[string]any) []endpointGroup {
	groups := make([]endpointGroup, 0, len(fdb)+len(neighbors)+len(peers))
	for _, row := range fdb {
		groups = append(groups, fdbEndpoint(row))
	}
	byAttachment := make(map[string]int, len(neighbors))
	for _, row := range neighbors {
		binding := row.Binding
		mac := usableNeighborMAC(row)
		id := schemaID("endpoint-linux-address-v1", row.ID)
		if mac != "" {
			id = schemaID("endpoint-linux-mac-v1/"+binding.managementID+"/"+binding.ovsUUID, mac)
		}
		evidence := endpointEvidence{
			Ref:    apitypes.Ref{Kind: "neighbor", ID: row.ID},
			Source: neighborSource, State: row.State, IP: row.IP, Family: row.Family, MAC: row.MAC,
		}
		if index, found := byAttachment[id]; found {
			groups[index].evidence = append(groups[index].evidence, evidence)
			continue
		}
		byAttachment[id] = len(groups)
		groups = append(groups, endpointGroup{
			id: id, basis: "neighbor", mac: mac, bridge: binding.bridge, port: binding.port,
			iface: neighborParent{binding.managementID, binding.request.Name}, evidence: []endpointEvidence{evidence},
		})
	}
	for _, peer := range peers {
		binding, frame := peer.binding, peer.frame
		seen := frame.ReceivedAt.UTC()
		evidence := endpointEvidence{
			Ref:    apitypes.Ref{Kind: "lldp_neighbor", ID: peer.id},
			Source: lldpSource, State: "observed", ChassisID: &frame.ChassisID, LLDPPortID: &frame.PortID,
			SystemName: frame.SystemName, TTLSeconds: frame.TTL, LastSeenAt: &seen,
		}
		groups = append(groups, endpointGroup{
			id: schemaID("endpoint-lldp-msap-v1", peer.id), basis: "lldp", bridge: binding.bridge, port: binding.port,
			iface: neighborParent{binding.managementID, binding.request.Name}, evidence: []endpointEvidence{evidence},
		})
	}
	// Ordered by the opaque, stable group ID. Neighbor evidence keeps the
	// source's deterministic Interface/IP order.
	slices.SortFunc(groups, func(a, b endpointGroup) int { return strings.Compare(a.id, b.id) })
	return groups
}

func usableNeighborMAC(row neighborEntry) string {
	if row.Binding.managementID == "" || row.Binding.ovsUUID == "" {
		return ""
	}
	// Upstream admission already validates that a non-empty MAC is a
	// six-byte unicast address. Only states which carry that evidence can
	// group IP addresses on a local Interface.
	switch row.State {
	case "reachable", "stale", "delay", "probe", "noarp", "permanent":
		return row.MAC
	}
	return ""
}

func fdbEndpoint(row fdbEntry) endpointGroup {
	evidence := endpointEvidence{
		Ref: apitypes.Ref{Kind: "fdb_entry", ID: row.id},
		Source: map[string]any{
			"provider_id": "ovs-vswitchd", "authority": "native-ovs-fdb",
			"freshness": "fresh", "confidence": "proven", "observed_at": row.observedAt.UTC(),
		},
		State: "static", MAC: row.MAC, VLAN: row.VLAN, OFPort: row.OFPort,
		PortName: row.PortName, PortResolution: row.portResolution,
	}
	if row.AgeSeconds != nil {
		seen := row.observedAt.Add(-time.Duration(*row.AgeSeconds) * time.Second).UTC()
		evidence.State, evidence.LastSeenAt = "learned", &seen
	}
	var port neighborParent
	if row.portResolution == "resolved" && row.PortName != nil {
		port = neighborParent{row.port.ManagementID, *row.PortName}
	}
	return endpointGroup{
		id: schemaID("endpoint-ovs-fdb-v1", row.id), basis: "fdb", mac: row.MAC, vlan: row.VLAN,
		bridge: neighborParent{row.bridge.binding.ManagementID, row.bridge.name}, port: port,
		evidence: []endpointEvidence{evidence},
	}
}

func (g endpointGroup) matchesText(filter string) bool {
	filter = strings.ToLower(filter)
	for _, value := range []string{g.basis, g.mac, g.bridge.name, g.port.name, g.iface.name} {
		if strings.Contains(strings.ToLower(value), filter) {
			return true
		}
	}
	for _, e := range g.evidence {
		for _, value := range []string{e.State, e.IP, e.Family, e.MAC, e.SystemName} {
			if strings.Contains(strings.ToLower(value), filter) {
				return true
			}
		}
		if e.ChassisID != nil && strings.Contains(strings.ToLower(e.ChassisID.Value), filter) ||
			e.LLDPPortID != nil && strings.Contains(strings.ToLower(e.LLDPPortID.Value), filter) ||
			e.PortName != nil && strings.Contains(strings.ToLower(*e.PortName), filter) {
			return true
		}
	}
	return false
}

func (g endpointGroup) hasAddress(ip string) bool {
	return ip == "" || slices.ContainsFunc(g.evidence, func(e endpointEvidence) bool { return e.IP == ip })
}

func (g endpointGroup) resource(sequence uint64, evidenceLimit int, preferredIP string) map[string]any {
	count := min(evidenceLimit, maxEndpointDetailEvidence, len(g.evidence))
	preview := g.evidence[:count]
	if count > 0 && preferredIP != "" {
		index := slices.IndexFunc(g.evidence, func(e endpointEvidence) bool { return e.IP == preferredIP })
		if index > 0 {
			preview = make([]endpointEvidence, 0, count)
			preview = append(preview, g.evidence[index])
			preview = append(preview, g.evidence[:min(index, count-1)]...)
			if index < count {
				preview = append(preview, g.evidence[index+1:count]...)
			}
		}
	}
	observation := map[string]any{
		"basis": g.basis, "evidence": preview, "evidence_total": len(g.evidence), "evidence_truncated": count < len(g.evidence),
	}
	if g.mac != "" {
		observation["mac"] = g.mac
	}
	if g.vlan != nil {
		observation["vlan"] = *g.vlan
	}
	for _, item := range []struct {
		kind string
		node neighborParent
	}{{"bridge", g.bridge}, {"port", g.port}, {"interface", g.iface}} {
		if item.node.id != "" {
			observation[item.kind+"_ref"] = apitypes.Ref{Kind: item.kind, ID: item.node.id}
			if item.node.name != "" {
				observation[item.kind+"_name"] = item.node.name
			}
		}
	}
	addresses := make([]string, 0, len(preview))
	for _, e := range preview {
		if e.IP != "" {
			addresses = append(addresses, e.IP)
		}
	}
	observation["addresses"] = addresses
	return map[string]any{
		"id": g.id, "resource_kind": "endpoint", "state": "observed",
		"sequence": strconv.FormatUint(sequence, 10), "allowed_actions": []string{},
		"source": g.evidence[0].Source, "endpoint": observation,
	}
}
