package candidate

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	QoSConfigure  = "qos.configure"
	QoSCapability = "ovs.qos.write"
	QoSMarker     = "ovs-webui-qos-execution"
	QoSQueueLimit = 32
	QoSMaxQueueID = 0xefff
	// OVS computes the implicit HFSC Queue ceiling with a uint32 bit rate.
	QoSMaxRateBPS = math.MaxUint32
)

type QoSQueueRate struct {
	ID      uint32 `json:"queue_id"`
	Minimum uint64 `json:"min_rate_bps"`
	Maximum uint64 `json:"max_rate_bps"`
}

type QoSRates struct {
	Type    string         `json:"type"`
	Maximum uint64         `json:"max_rate_bps"`
	Queues  []QoSQueueRate `json:"queues"`
}

type QoSChange struct {
	Before       QoSRates `json:"before"`
	After        QoSRates `json:"after"`
	QoS          Binding  `json:"qos"`
	Bridge       Binding  `json:"bridge"`
	Interface    Binding  `json:"interface"`
	Compensating bool     `json:"compensating,omitempty"`
}

type QoSPort struct {
	Binding, QoS, Bridge, Interface Binding
	Rates                           QoSRates
	QueueUUIDs                      map[uint32]string
	Name                            string
	IfIndex, MTU                    int
	Dependency, Reason              string
}

func ValidQoSRates(r QoSRates, mtu int) bool {
	if r.Type != "linux-htb" && r.Type != "linux-hfsc" || mtu < 68 || mtu > 65535 ||
		!validQoSRate(r.Maximum) || r.Maximum < uint64(mtu)*8 || r.Queues == nil || len(r.Queues) > QoSQueueLimit {
		return false
	}
	seen := make(map[uint32]bool, len(r.Queues))
	for _, q := range r.Queues {
		if q.ID > QoSMaxQueueID || seen[q.ID] || !validQoSRate(q.Minimum) || !validQoSRate(q.Maximum) ||
			q.Minimum > q.Maximum || q.Maximum > r.Maximum || r.Type == "linux-htb" && q.Minimum < uint64(mtu)*8 {
			return false
		}
		seen[q.ID] = true
	}
	return len(r.Queues) < QoSQueueLimit || seen[0]
}

func validQoSRate(value uint64) bool { return value > 0 && value <= QoSMaxRateBPS && value%8 == 0 }

func sameQoSLayout(a, b QoSRates) bool {
	return a.Type == b.Type && slices.EqualFunc(a.Queues, b.Queues, func(x, y QoSQueueRate) bool { return x.ID == y.ID })
}

func equalQoSRates(a, b QoSRates) bool {
	return a.Type == b.Type && a.Maximum == b.Maximum && slices.Equal(a.Queues, b.Queues)
}

func qosProblem(i StoredIntent, s Snapshot) string {
	p := s.QoSPorts[i.Object.ManagementID]
	m := i.QoS
	switch {
	case m == nil || i.Object.Table != "Port":
		return "INVALID_QOS_RATES"
	case i.Object.Generation != s.Generation:
		return "GENERATION_RECONCILIATION_REQUIRED"
	case i.Schema != s.Schema:
		return "SCHEMA_CHANGED"
	case p.Binding != i.Object || p.QoS != m.QoS || p.Interface != m.Interface || p.Bridge != m.Bridge:
		return "OBJECT_BINDING_CHANGED"
	case p.Reason != "":
		return p.Reason
	case i.Dependency != p.Dependency:
		return "QOS_DEPENDENCY_CHANGED"
	case !equalQoSRates(m.Before, p.Rates):
		return "FIELD_CONFLICT"
	case !ValidQoSRates(m.After, p.MTU) || !sameQoSLayout(m.Before, m.After):
		return "INVALID_QOS_RATES"
	}
	return ""
}

func stageQoS(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 {
		return c, apitypes.Fail(422, "QOS_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	p, granted := s.QoSPorts[in.Object.ManagementID]
	if in.Operation != QoSConfigure || in.QoSQueues == nil || in.Object.Table != "Port" || !apitypes.ManagementID(in.ID) ||
		in.Name != "" || in.MTURequest != 0 || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_QOS_RATES")
	}
	if c.Generation != nil && *c.Generation != s.Generation || in.Object.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	if !granted {
		return c, apitypes.Fail(409, "EXCLUSIVE_QOS_AUTHORITY_REQUIRED")
	}
	after := QoSRates{Type: in.QoSType, Maximum: in.QoSMaximum, Queues: slices.Clone(in.QoSQueues)}
	slices.SortFunc(after.Queues, func(a, b QoSQueueRate) int { return cmp.Compare(a.ID, b.ID) })
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency,
		Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		QoS: &QoSChange{Before: p.Rates, After: after, QoS: p.QoS, Bridge: p.Bridge, Interface: p.Interface}}
	if len(c.Intents) == 1 {
		i = c.Intents[0]
		if i.Operation != in.Operation || i.ID != in.ID || i.Object != in.Object || i.QoS == nil {
			return c, apitypes.Fail(409, "QOS_RESTAGE_REQUIRED")
		}
		prior := *i.QoS
		prior.After = after
		i.QoS = &prior
	}
	if reason := qosProblem(i, s); reason != "" {
		return c, apitypes.Fail(409, reason)
	}
	if equalQoSRates(i.QoS.Before, after) {
		return c, apitypes.Fail(422, "QOS_RATES_UNCHANGED")
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func qosDiff(i StoredIntent, s Snapshot) []Diff {
	if i.QoS == nil {
		return nil
	}
	p := s.QoSPorts[i.Object.ManagementID]
	m := i.QoS
	conflict := qosProblem(i, s) != ""
	makeDiff := func(field string, before, after uint64, current any) Diff {
		return Diff{Object: i.Object, Field: field, Before: before, After: after, Current: current,
			Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: conflict}
	}
	var maximum any
	if p.Rates.Maximum > 0 {
		maximum = p.Rates.Maximum
	}
	out := []Diff{makeDiff("QoS maximum rate (bit/s)", m.Before.Maximum, m.After.Maximum, maximum)}
	for n, before := range m.Before.Queues {
		if n >= len(m.After.Queues) || m.After.Queues[n].ID != before.ID {
			break
		}
		after := m.After.Queues[n]
		var minNow, maxNow any
		if index := slices.IndexFunc(p.Rates.Queues, func(q QoSQueueRate) bool { return q.ID == before.ID }); index >= 0 {
			minNow, maxNow = p.Rates.Queues[index].Minimum, p.Rates.Queues[index].Maximum
		}
		out = append(out,
			makeDiff(fmt.Sprintf("Queue %d minimum rate (bit/s)", before.ID), before.Minimum, after.Minimum, minNow),
			makeDiff(fmt.Sprintf("Queue %d maximum rate (bit/s)", before.ID), before.Maximum, after.Maximum, maxNow))
	}
	return out
}
