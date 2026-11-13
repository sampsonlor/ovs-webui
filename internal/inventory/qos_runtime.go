package inventory

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	QoSMaxQueueID        = 0xefff
	QoSRuntimeQueueLimit = 32
)

type QoSRuntimeRequest struct {
	Device   DeviceRequest
	Kind     string
	QueueIDs []uint32
}

type QoSQueueCounters struct {
	QueueID uint32  `json:"queue_id"`
	Bytes   *string `json:"bytes"`
	Packets *string `json:"packets"`
	Drops   *string `json:"drops"`
}

type QoSRuntimeSample struct {
	Scheduler  string
	RootKind   string
	Reason     string
	ObservedAt time.Time
	Queues     []QoSQueueCounters
}

type QoSObserver interface {
	ObserveQoS(context.Context, QoSRuntimeRequest) QoSRuntimeSample
}

func (s *Service) SetQoSObserver(p QoSObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qosObserver = p
}

type qosRuntimeBinding struct {
	request                                        QoSRuntimeRequest
	port                                           apitypes.Ref
	portUUID, bridgeID, interfaceID, queueRevision string
}

func bindQoSRuntime(v *view, b Binding, ports qosBindings) (qosRuntimeBinding, string) {
	var target qosRuntimeBinding
	if !ports.complete {
		return target, "QOS_PORT_BINDING_UNKNOWN"
	}
	bound := ports.ports[b.UUID]
	switch len(bound) {
	case 0:
		return target, "QOS_PORT_UNBOUND"
	case 1:
		target.port, target.portUUID = bound[0].Ref, bound[0].uuid
	default:
		return target, "QOS_SHARED_PORT_BINDING"
	}
	qos := v.observation.Rows["QoS"][b.UUID]
	switch textValue(qos.Values["type"]) {
	case "linux-htb":
		target.request.Kind = "htb"
	case "linux-hfsc":
		target.request.Kind = "hfsc"
	default:
		return target, "QOS_DISCIPLINE_UNSUPPORTED"
	}
	port := v.observation.Rows["Port"][target.portUUID]
	members, known := port.Values["interfaces"].([]any)
	bridge, found := parent(v, "Bridge", "ports", target.portUUID)
	bridgeBinding := v.decision.Bindings[Key("Bridge", bridge.UUID)]
	datapath, datapathKnown := bridge.Values["datapath_type"].(string)
	if !known || len(members) != 1 || !found || bridgeBinding.State != "active" || !datapathKnown || datapath != "" && datapath != "system" {
		return target, "QOS_DEVICE_BINDING_UNPROVEN"
	}
	ifaceID, _ := members[0].(string)
	iface := v.observation.Rows["Interface"][ifaceID]
	ifaceBinding := v.decision.Bindings[Key("Interface", ifaceID)]
	dev, eligible := deviceRequest(iface)
	ofport, err := strconv.ParseUint(textValue(iface.Values["ofport"]), 10, 16)
	if !eligible || ifaceBinding.State != "active" || dev.Name != bound[0].Name || err != nil || ofport == 0 || ofport >= 65534 {
		return target, "QOS_DEVICE_BINDING_UNPROVEN"
	}
	queues, _, complete := readQoSQueues(v, qos, true, true)
	if !complete {
		return target, "QOS_QUEUE_MAPPING_UNKNOWN"
	}
	target.request.QueueIDs = []uint32{0}
	for _, queue := range queues {
		if queue.ID > QoSMaxQueueID {
			return target, "QOS_QUEUE_ID_UNSUPPORTED"
		}
		if queue.ID != 0 {
			target.request.QueueIDs = append(target.request.QueueIDs, queue.ID)
		}
	}
	if len(target.request.QueueIDs) > QoSRuntimeQueueLimit {
		return target, "QOS_RUNTIME_QUEUE_LIMIT"
	}
	target.request.Device = dev
	target.bridgeID = bridgeBinding.ManagementID
	target.interfaceID = ifaceBinding.ManagementID
	// Ignore unrelated rows and ephemeral OVS observations when rechecking.
	target.queueRevision = Digest([]any{qos.Values["other_config"], queues, ofport, iface.Values["type"]})
	return target, ""
}

func qosRuntimeUnavailable(reason string) map[string]any {
	return map[string]any{
		"scheduler": "unavailable", "reason": reason, "root_kind": nil, "port_ref": nil,
		"source": qosRuntimeSource(nil, "unavailable", "unknown"), "queues": []QoSQueueCounters{},
	}
}

func qosRuntimeSource(at any, freshness, confidence string) map[string]any {
	return map[string]any{"provider_id": "linux", "authority": "linux-netlink-observation", "observed_at": at, "freshness": freshness, "confidence": confidence}
}

func (s *Service) readQoSRuntime(ctx context.Context, v *view, b Binding, fresh string, ports qosBindings) map[string]any {
	if fresh != "fresh" {
		return qosRuntimeUnavailable("OVS_ASSOCIATION_STALE")
	}
	target, reason := bindQoSRuntime(v, b, ports)
	if reason != "" {
		return qosRuntimeUnavailable(reason)
	}
	s.mu.RLock()
	observer := s.qosObserver
	s.mu.RUnlock()
	if observer == nil {
		return qosRuntimeUnavailable("LINUX_PROVIDER_UNAVAILABLE")
	}
	sample := observer.ObserveQoS(ctx, target.request)
	s.mu.RLock()
	current, failure := s.current, s.failure
	s.mu.RUnlock()
	if ctx.Err() != nil || current == nil || failure != "" || current.decision.State != "confirmed" ||
		current.decision.Generation != v.decision.Generation || current.decision.Bindings[Key("QoS", b.UUID)] != b {
		return qosRuntimeUnavailable("QOS_ASSOCIATION_CHANGED")
	}
	age := s.now().Sub(current.observation.Evidence.ObservedAt)
	if age < 0 || age > FreshFor {
		return qosRuntimeUnavailable("OVS_ASSOCIATION_STALE")
	}
	if current != v {
		next, reason := bindQoSRuntime(current, b, indexQoSPorts(current))
		if reason != "" || next.port != target.port || next.portUUID != target.portUUID ||
			next.bridgeID != target.bridgeID || next.interfaceID != target.interfaceID ||
			next.request.Device != target.request.Device || next.request.Kind != target.request.Kind ||
			!slices.Equal(next.request.QueueIDs, target.request.QueueIDs) || next.queueRevision != target.queueRevision {
			return qosRuntimeUnavailable("QOS_ASSOCIATION_CHANGED")
		}
	}
	if sample.Scheduler == "unavailable" {
		return qosRuntimeUnavailable(sample.Reason)
	}
	age = s.now().Sub(sample.ObservedAt)
	if sample.ObservedAt.IsZero() || age < 0 || age > FreshFor {
		return qosRuntimeUnavailable("LINUX_QOS_OBSERVATION_EXPIRED")
	}
	var root any
	if sample.RootKind != "" {
		root = sample.RootKind
	}
	if sample.Queues == nil {
		sample.Queues = []QoSQueueCounters{}
	}
	return map[string]any{
		"scheduler": sample.Scheduler, "reason": sample.Reason, "root_kind": root, "port_ref": target.port,
		"source": qosRuntimeSource(sample.ObservedAt.UTC(), "fresh", "proven"), "queues": sample.Queues,
	}
}
