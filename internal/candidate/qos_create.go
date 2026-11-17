package candidate

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	QoSCreate         = "qos.create"
	QoSProfileName    = "ovs-webui-qos-profile"
	QoSProfileMarker  = "ovs-webui-qos-profile-execution"
	QoSCreateMaxNames = 64
)

var qosProfileName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func ValidQoSProfileName(name string) bool { return qosProfileName.MatchString(name) }

type QoSCreation struct {
	Name          string             `json:"name"`
	Rates         QoSRates           `json:"rates"`
	Root          string             `json:"root_uuid"`
	Queues        map[string]Binding `json:"queue_bindings"`
	BeforePresent bool               `json:"before_present"`
	AfterPresent  bool               `json:"after_present"`
	Compensating  bool               `json:"compensating,omitempty"`
}

type QoSCreateState struct {
	Root      string
	Supported bool
	Capacity  int
	Names     map[string]uint64
	Allowed   map[string]bool
	Objects   map[string]Binding
}

func stageQoSCreation(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) != 0 {
		return c, apitypes.Fail(409, "QOS_RESTAGE_REQUIRED")
	}
	in := cmd.Intents[0]
	if in.Operation != QoSCreate || !apitypes.ManagementID(in.ID) || in.Object != (Binding{}) ||
		in.Name == "" || in.MTURequest != 0 || in.VLANID != 0 || in.Mode != "" || in.LACP != "" || in.Fallback != "" ||
		len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return c, apitypes.Fail(422, "INVALID_QOS_CREATION")
	}
	if in.QoSGeneration != s.Generation || s.Generation == "" || c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	rates := QoSRates{Type: in.QoSType, Maximum: in.QoSMaximum, Queues: slices.Clone(in.QoSQueues)}
	if !ValidQoSProfileName(in.Name) || rates.Type != "linux-hfsc" || len(rates.Queues) == 0 || !ValidQoSRates(rates, 1500) {
		return c, apitypes.Fail(422, "INVALID_QOS_CREATION")
	}
	slices.SortFunc(rates.Queues, func(a, b QoSQueueRate) int { return cmp.Compare(a.ID, b.ID) })
	mint := func(table string) Binding {
		return Binding{Table: table, OVSUUID: repository.NewID(), ManagementID: repository.NewID(), Generation: s.Generation}
	}
	queues := make(map[string]Binding, len(rates.Queues))
	for _, q := range rates.Queues {
		queues[strconv.FormatUint(uint64(q.ID), 10)] = mint("Queue")
	}
	i := StoredIntent{
		ID: in.ID, Operation: QoSCreate, Object: mint("QoS"), Schema: s.Schema,
		Dependency: Digest([]string{s.QoSCreate.Root, in.Name}), Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		QoSCreation: &QoSCreation{Name: in.Name, Rates: rates, Root: s.QoSCreate.Root, Queues: queues, AfterPresent: true},
	}
	if reason := qosCreationProblem(i, s); reason != "" {
		return c, apitypes.Fail(409, reason)
	}
	c.Intents, c.Generation, c.BaseRevision = []StoredIntent{i}, &s.Generation, &s.Revision
	return c, nil
}

func qosCreationProblem(i StoredIntent, s Snapshot) string {
	m, target := i.QoSCreation, s.QoSCreate
	switch {
	case m == nil || !ValidQoSProfileName(m.Name) || m.Rates.Type != "linux-hfsc" || len(m.Rates.Queues) == 0 ||
		!ValidQoSRates(m.Rates, 1500) || len(m.Rates.Queues) != len(m.Queues):
		return "INVALID_QOS_CREATION"
	case i.Object.Generation != s.Generation:
		return "GENERATION_RECONCILIATION_REQUIRED"
	case i.Schema != s.Schema || !target.Supported:
		return "QOS_CREATION_SCHEMA_UNSUPPORTED"
	case m.Root != target.Root || i.Dependency != Digest([]string{target.Root, m.Name}):
		return "QOS_CREATION_ROOT_CHANGED"
	case !m.Compensating && !m.BeforePresent && !target.Allowed[m.Name]:
		return "QOS_CREATION_AUTHORITY_REQUIRED"
	case !m.Compensating && !m.BeforePresent && target.Names[m.Name] != 0:
		return "QOS_CREATION_NAME_IN_USE"
	case !m.Compensating && !m.BeforePresent && target.Capacity < len(m.Rates.Queues)+1:
		return "QOS_CREATION_CAPACITY"
	}
	bindings := CreationBindings(i)
	if i.Object.Table != "QoS" || len(bindings) != len(m.Rates.Queues)+1 {
		return "INVALID_QOS_CREATION"
	}
	for index, b := range bindings {
		if (index > 0 && b.Table != "Queue") || !apitypes.ManagementID(b.ManagementID) || !apitypes.UUID(b.OVSUUID) || b.Generation != s.Generation {
			return "INVALID_QOS_CREATION"
		}
		current, present := target.Objects[b.OVSUUID]
		if present != m.BeforePresent || present && current != b {
			return "QOS_CREATION_IDENTITY_CHANGED"
		}
	}
	return ""
}

func qosCreationDiff(i StoredIntent, s Snapshot) []Diff {
	m := i.QoSCreation
	if m == nil {
		return nil
	}
	conflict := qosCreationProblem(i, s) != ""
	show := func(field string, value any) Diff {
		d := Diff{Object: i.Object, Field: field, Authority: "ovsdb-configuration",
			Operation: i.Operation, IntentID: i.ID, Conflict: conflict}
		if m.BeforePresent {
			d.Before, d.Current = value, value
		}
		if m.AfterPresent {
			d.After = value
		}
		return d
	}
	out := []Diff{
		show("QoS profile", m.Name), show("QoS discipline", m.Rates.Type),
		show("QoS maximum rate (bit/s)", m.Rates.Maximum),
	}
	for _, q := range m.Rates.Queues {
		field := qosQueueField(q.ID)
		out = append(out, show(field+" minimum rate (bit/s)", q.Minimum), show(field+" maximum rate (bit/s)", q.Maximum))
	}
	return out
}
