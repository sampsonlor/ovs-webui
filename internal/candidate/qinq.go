package candidate

import "slices"

// QinQContext captures immutable dependencies outside the four VLAN columns.
// A nil EtherType means the native key is absent (OVS defaults to 802.1ad).
// Keeping this optional preserves serialization of older signed field journals.
type QinQContext struct {
	VLANLimit      string  `json:"vlan_limit"`
	DatapathUUID   string  `json:"datapath_uuid"`
	MaxVLANHeaders string  `json:"max_vlan_headers"`
	Dependency     string  `json:"dependency_revision"`
	EtherType      *string `json:"ethertype"`
}

func UsesQinQ(i StoredIntent) bool {
	return i.Operation == "port.vlan.set" && (i.QinQ != nil || qinqMode(i.Before) || qinqMode(i.Value))
}

func qinqMode(v VLAN) bool { return v.Mode != nil && *v.Mode == "dot1q-tunnel" }

func cloneQinQ(q *QinQContext) *QinQContext {
	if q == nil {
		return nil
	}
	copy := *q
	copy.EtherType = copyString(q.EtherType)
	return &copy
}

// An absent mode is preserved for compensation. Validate its effective native
// mode without writing that default back, and reject ignored/noncanonical data.
func vlanOriginalSupported(v VLAN) bool {
	if v.Mode == nil {
		mode := "trunk"
		if v.Tag != nil {
			mode = "access"
		}
		v.Mode = &mode
	}
	return inputValid(v)
}

func QinQEditable(p Port) bool {
	return p.Known && p.SchemaSupported && p.Authority == "local-managed" &&
		p.QinQSupported && p.QinQ != nil && slices.Contains(p.Modes, "dot1q-tunnel") && vlanOriginalSupported(p.VLAN)
}

func qinqChecks(i StoredIntent, p Port) []Gate {
	out := []Gate{}
	if !p.QinQSupported || p.QinQ == nil || !slices.Contains(p.Modes, "dot1q-tunnel") {
		out = append(out, gate("QINQ_CONTEXT_UNSUPPORTED", "blocked", i.ID))
	}
	if i.QinQ == nil || p.QinQ == nil || Digest(i.QinQ) != Digest(p.QinQ) {
		out = append(out, gate("QINQ_DEPENDENCY_CHANGED", "blocked", i.ID))
	}
	if !vlanOriginalSupported(i.Before) || !vlanOriginalSupported(i.Value) {
		out = append(out, gate("QINQ_NATIVE_SEMANTICS_UNPROVEN", "blocked", i.ID))
	}
	if qinqMode(i.Value) && len(i.Value.CVLANs) == 0 {
		out = append(out, gate("EMPTY_CVLANS_MEANS_ALL_CUSTOMER_VLANS", "allowed", i.ID))
	}
	out = append(out, gate("QINQ_SERVICE_TAG_AND_PRESERVED_TPID_REVIEW", "allowed", i.ID))
	return out
}
