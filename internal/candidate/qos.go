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
	QoSDetach     = "qos.detach"
	QoSAttach     = "qos.attach"
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
	Before        QoSRates `json:"before"`
	After         QoSRates `json:"after"`
	QoS           Binding  `json:"qos"`
	Bridge        Binding  `json:"bridge"`
	Interface     Binding  `json:"interface"`
	Compensating  bool     `json:"compensating,omitempty"`
	BeforeUnbound bool     `json:"before_unbound,omitempty"`
	AfterUnbound  bool     `json:"after_unbound,omitempty"`
}

type QoSPort struct {
	Binding, QoS, Bridge, Interface Binding
	Rates                           QoSRates
	QueueUUIDs                      map[uint32]string
	Name                            string
	IfIndex, MTU                    int
	Dependency, Reason              string
	Unbound                         bool `json:",omitempty"`
}

func IsQoSBindingOperation(op string) bool { return op == QoSDetach || op == QoSAttach }

func IsQoSOperation(op string) bool {
	return op == QoSConfigure || op == QoSCreate || IsQoSBindingOperation(op)
}

func qosQueueField(id uint32) string { return fmt.Sprintf("Queue %d", id) }

func (s Snapshot) QoSTarget(operation string, port Binding) QoSPort {
	switch operation {
	case QoSDetach:
		return s.QoSDetachPorts[port.ManagementID]
	case QoSAttach:
		return s.QoSAttachPorts[port.ManagementID]
	default:
		return s.QoSPorts[port.ManagementID]
	}
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
	p := s.QoSTarget(i.Operation, i.Object)
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
	case p.Unbound != m.BeforeUnbound:
		return "QOS_BINDING_CHANGED"
	case i.Dependency != p.Dependency:
		return "QOS_DEPENDENCY_CHANGED"
	case !equalQoSRates(m.Before, p.Rates):
		return "FIELD_CONFLICT"
	case !ValidQoSRates(m.After, p.MTU) || !sameQoSLayout(m.Before, m.After):
		return "INVALID_QOS_RATES"
	case IsQoSBindingOperation(i.Operation) && (m.After.Type != "linux-hfsc" || !equalQoSRates(m.Before, m.After)):
		return "INVALID_QOS_RATES"
	}
	return ""
}

func stageQoS(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 {
		return c, apitypes.Fail(422, "QOS_SINGLE_INTENT_REQUIRED")
	}
	in := cmd.Intents[0]
	p := s.QoSTarget(in.Operation, in.Object)
	if !IsQoSOperation(in.Operation) || in.Object.Table != "Port" || !apitypes.ManagementID(in.ID) ||
		in.Name != "" || in.MTURequest != 0 || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_QOS_RATES")
	}
	if c.Generation != nil && *c.Generation != s.Generation || in.Object.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	if p.Binding.ManagementID != in.Object.ManagementID {
		return c, apitypes.Fail(409, "EXCLUSIVE_QOS_AUTHORITY_REQUIRED")
	}
	after := p.Rates
	binding := IsQoSBindingOperation(in.Operation)
	if binding {
		if in.QoSBinding == nil || *in.QoSBinding != p.QoS || p.Unbound != (in.Operation == QoSAttach) {
			return c, apitypes.Fail(409, "QOS_BINDING_CHANGED")
		}
	} else {
		if in.QoSQueues == nil {
			return c, apitypes.Fail(422, "INVALID_QOS_RATES")
		}
		after = QoSRates{Type: in.QoSType, Maximum: in.QoSMaximum, Queues: slices.Clone(in.QoSQueues)}
		slices.SortFunc(after.Queues, func(a, b QoSQueueRate) int { return cmp.Compare(a.ID, b.ID) })
	}
	i := StoredIntent{ID: in.ID, Operation: in.Operation, Object: in.Object, Schema: s.Schema, Dependency: p.Dependency,
		Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		QoS: &QoSChange{Before: p.Rates, After: after, QoS: p.QoS, Bridge: p.Bridge, Interface: p.Interface,
			BeforeUnbound: in.Operation == QoSAttach, AfterUnbound: in.Operation == QoSDetach}}
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
	if !binding && equalQoSRates(i.QoS.Before, after) {
		return c, apitypes.Fail(422, "QOS_RATES_UNCHANGED")
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func qosDiff(i StoredIntent, s Snapshot) []Diff {
	if i.QoS == nil {
		return nil
	}
	p := s.QoSTarget(i.Operation, i.Object)
	m := i.QoS
	conflict := qosProblem(i, s) != ""
	makeDiff := func(field string, before, after, current any) Diff {
		return Diff{Object: i.Object, Field: field, Before: before, After: after, Current: current,
			Authority: "ovsdb-configuration", Operation: i.Operation, IntentID: i.ID, Conflict: conflict}
	}
	var out []Diff
	if IsQoSBindingOperation(i.Operation) {
		label := func(unbound bool) string {
			if unbound {
				return "unbound"
			}
			return m.QoS.ManagementID
		}
		var current any
		if p.Binding == i.Object && p.QoS == m.QoS && p.Reason == "" {
			current = label(p.Unbound)
		}
		out = append(out, makeDiff("Port QoS binding", label(m.BeforeUnbound), label(m.AfterUnbound), current))
		if i.Operation == QoSDetach {
			return out
		}
	}
	var maximum any
	if !p.Unbound && p.Rates.Maximum > 0 {
		maximum = p.Rates.Maximum
	}
	rate := func(value uint64, unbound bool) any {
		if unbound {
			return nil
		}
		return value
	}
	out = append(out, makeDiff("QoS maximum rate (bit/s)", rate(m.Before.Maximum, m.BeforeUnbound), rate(m.After.Maximum, m.AfterUnbound), maximum))
	for n, before := range m.Before.Queues {
		if n >= len(m.After.Queues) || m.After.Queues[n].ID != before.ID {
			break
		}
		after := m.After.Queues[n]
		var minNow, maxNow any
		if index := slices.IndexFunc(p.Rates.Queues, func(q QoSQueueRate) bool { return q.ID == before.ID }); !p.Unbound && index >= 0 {
			minNow, maxNow = p.Rates.Queues[index].Minimum, p.Rates.Queues[index].Maximum
		}
		out = append(out,
			makeDiff(qosQueueField(before.ID)+" minimum rate (bit/s)", rate(before.Minimum, m.BeforeUnbound), rate(after.Minimum, m.AfterUnbound), minNow),
			makeDiff(qosQueueField(before.ID)+" maximum rate (bit/s)", rate(before.Maximum, m.BeforeUnbound), rate(after.Maximum, m.AfterUnbound), maxNow))
	}
	return out
}
