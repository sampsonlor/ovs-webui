package inventory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apicontract"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type qosObserverFunc func(context.Context, QoSRuntimeRequest) QoSRuntimeSample

func (f qosObserverFunc) ObserveQoS(ctx context.Context, req QoSRuntimeRequest) QoSRuntimeSample {
	return f(ctx, req)
}

func TestQoSRuntimeReadRechecksNativeBinding(t *testing.T) {
	s, observation, decision, claims := fixture(t)
	observation.Schema.QoSConfiguration = true
	qosID := repository.NewID()
	binding := Binding{Table: "QoS", UUID: qosID, ManagementID: repository.NewID(), State: "active"}
	decision.Bindings[Key("QoS", qosID)] = binding
	observation.Rows["QoS"] = map[string]Row{qosID: {UUID: qosID, Values: map[string]any{"type": "linux-htb", "queues": map[string]any{}}}}
	var port, bridge, iface Row
	for _, row := range observation.Rows["Bridge"] {
		bridge = row
	}
	for _, row := range observation.Rows["Port"] {
		port = row
	}
	for _, row := range observation.Rows["Interface"] {
		iface = row
		break
	}
	bridge.Values["datapath_type"] = "system"
	port.Values["name"], port.Values["interfaces"] = "qos-test", []any{iface.UUID}
	port.Configuration = map[string]any{"qos": []any{qosID}}
	observation.Rows["Port"][port.UUID] = port
	iface.Values["type"], iface.Values["name"], iface.Values["ofport"], iface.Values["ifindex"] = "system", "qos-test", []any{"3"}, []any{"4"}
	s.install(observation, decision)
	contract, err := apicontract.New()
	if err != nil {
		t.Fatal(err)
	}
	op, path, _ := contract.Match("GET", "/api/v1/qos/"+binding.ManagementID)
	read := func() map[string]any {
		t.Helper()
		item, err := s.Read(context.Background(), op.ID, path, nil, claims)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := op.ValidateResponse(200, body); err != nil {
			t.Fatal(err)
		}
		return item.(map[string]any)["linux_qos"].(map[string]any)
	}
	calls, change := 0, false
	s.SetQoSObserver(qosObserverFunc(func(_ context.Context, req QoSRuntimeRequest) QoSRuntimeSample {
		calls++
		if req.Kind != "htb" || req.Device.Name != "qos-test" || req.Device.IfIndex != 4 || len(req.QueueIDs) != 1 || req.QueueIDs[0] != 0 {
			t.Fatal("incorrect native binding or implicit default queue", req)
		}
		if change {
			s.Unavailable("OVSDB_MONITOR_UNAVAILABLE")
		}
		zero := "0"
		return QoSRuntimeSample{Scheduler: "observed", RootKind: "htb", ObservedAt: s.now(), Queues: []QoSQueueCounters{{QueueID: 0, Packets: &zero}}}
	}))
	if got := read(); got["scheduler"] != "observed" || len(got["queues"].([]QoSQueueCounters)) != 1 {
		t.Fatal("runtime observation unavailable", got)
	}
	change = true
	if got := read(); got["reason"] != "QOS_ASSOCIATION_CHANGED" || len(got["queues"].([]QoSQueueCounters)) != 0 {
		t.Fatal("published stale counters", got)
	}
	if got := read(); got["reason"] != "OVS_ASSOCIATION_STALE" || calls != 2 {
		t.Fatal("queried kernel with stale native identity", got, calls)
	}
}
