package inventory

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

// Each grant binds the reviewed Port to one immutable QoS identity.
func (s *Service) SetLocalQoS(grants []string) error {
	if len(grants) > 128 {
		return apitypes.Fail(422, "INVALID_QOS_AUTHORITY")
	}
	allow := make(map[string]string, len(grants))
	for _, grant := range grants {
		port, qos, _ := strings.Cut(grant, ":")
		if !apitypes.ManagementID(port) || !apitypes.ManagementID(qos) || allow[port] != "" {
			return apitypes.Fail(422, "INVALID_QOS_AUTHORITY")
		}
		allow[port] = qos
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localQoS = allow
	return nil
}

func projectQoS(v *view, allow map[string]string, out *candidate.Snapshot, bindings []candidate.Binding) {
	out.QoSPorts = map[string]candidate.QoSPort{}
	var ports qosBindings
	for _, b := range bindings {
		if b.Table != "Port" || allow[b.ManagementID] == "" {
			continue
		}
		if ports.ports == nil {
			ports = indexQoSPorts(v)
		}
		out.QoSPorts[b.ManagementID] = qosRateTarget(v, b.OVSUUID, allow, ports)
	}
	if len(out.QoSPorts) > 0 {
		out.Revision = Digest([]any{out.Revision, out.QoSPorts})
	}
}

func qosRateTarget(v *view, portID string, allow map[string]string, ports qosBindings) (p candidate.QoSPort) {
	bind := func(table, id string) candidate.Binding {
		b := v.decision.Bindings[Key(table, id)]
		if b.State != "active" {
			return candidate.Binding{}
		}
		return candidate.Binding{Table: table, OVSUUID: id, ManagementID: b.ManagementID, Generation: v.decision.Generation}
	}
	p.Binding = bind("Port", portID)
	port := v.observation.Rows["Port"][portID]
	qosID, _ := qosPortTarget(port)
	p.QoS = bind("QoS", qosID)
	p.Reason = "EXCLUSIVE_QOS_AUTHORITY_REQUIRED"
	if p.QoS.ManagementID == "" || allow[p.Binding.ManagementID] != p.QoS.ManagementID {
		return
	}
	p.Reason = "QOS_WRITE_SCHEMA_UNSUPPORTED"
	if !v.observation.Schema.QoSRateWrite {
		return
	}
	qos := v.observation.Rows["QoS"][qosID]
	runtime, reason := bindQoSRuntime(v, v.decision.Bindings[Key("QoS", qosID)], ports)
	if reason != "" || runtime.portUUID != portID {
		p.Reason = "QOS_DEVICE_BINDING_UNPROVEN"
		return
	}
	p.Name, p.IfIndex = runtime.request.Device.Name, runtime.request.Device.IfIndex
	bridge, _ := parent(v, "Bridge", "ports", portID)
	ifaceID := refs(port.Values["interfaces"])[0]
	iface := v.observation.Rows["Interface"][ifaceID]
	p.Bridge, p.Interface = bind("Bridge", bridge.UUID), bind("Interface", ifaceID)
	root := v.observation.Rows["Open_vSwitch"][v.observation.Evidence.Root]
	rootOptions, rootKnown := root.Configuration["other_config"].(map[string]any)
	ifaceType := iface.Values["type"]
	p.MTU, _ = strconv.Atoi(textValue(iface.Values["mtu"]))
	p.Reason = "QOS_DEVICE_BINDING_UNPROVEN"
	if !rootKnown || rootOptions["hw-offload"] != nil && rootOptions["hw-offload"] != "false" ||
		!slices.Contains(refs(root.Values["bridges"]), bridge.UUID) || p.Name == bridge.Values["name"] ||
		ifaceType != "" && ifaceType != "system" || !qosEmptySet(iface.Values["error"]) ||
		!qosEmptySet(bridge.Configuration["controller"]) || !qosEmptySet(bridge.Configuration["mirrors"]) ||
		iface.InterfaceOptionsEmpty == nil || !*iface.InterfaceOptionsEmpty {
		return
	}
	for _, row := range []Row{root, bridge, port, iface, qos} {
		if !qosLocalLabels(row) {
			p.Reason = "EXTERNALLY_CONTROLLED"
			return
		}
	}
	qosOptions, ok := qos.Configuration["other_config"].(map[string]any)
	queueMap, _, complete := readQoSQueues(v, qos, true, true)
	p.Reason = "QOS_NATIVE_RATES_UNSUPPORTED"
	if !ok || !complete {
		return
	}
	p.Rates = candidate.QoSRates{Type: textValue(qos.Values["type"]), Maximum: qosRateNumber(qosOptions["max-rate"]), Queues: []candidate.QoSQueueRate{}}
	p.QueueUUIDs = make(map[uint32]string, len(queueMap))
	deps := []any{
		root.UUID, root.Configuration, qosPersistentDependency(bridge), qosPersistentDependency(port),
		qosPersistentDependency(iface), p.IfIndex, iface.Values["ofport"], p.MTU,
		p.QoS, qos.Values["queues"], qos.Values["type"], qos.Configuration["external_ids"], qosOtherOptions(qosOptions, "max-rate"),
	}
	mapping := qos.Values["queues"].(map[string]any)
	for _, q := range queueMap {
		uuid, canonical := mapping[strconv.FormatUint(uint64(q.ID), 10)].(string)
		if !canonical {
			return
		}
		row := v.observation.Rows["Queue"][uuid]
		if !qosLocalLabels(row) {
			p.Reason = "EXTERNALLY_CONTROLLED"
			return
		}
		config, known := row.Configuration["other_config"].(map[string]any)
		if !known || q.DSCPState == "unknown" {
			return
		}
		if p.Rates.Type == "linux-htb" && (config["burst"] != nil || config["priority"] != nil) {
			return
		}
		p.QueueUUIDs[q.ID] = uuid
		p.Rates.Queues = append(p.Rates.Queues, candidate.QoSQueueRate{ID: q.ID, Minimum: qosRateNumber(config["min-rate"]), Maximum: qosRateNumber(config["max-rate"])})
		deps = append(deps, q.Ref, uuid, row.Values["dscp"], row.Configuration["external_ids"], qosOtherOptions(config, "min-rate", "max-rate"))
	}
	if !candidate.ValidQoSRates(p.Rates, p.MTU) {
		return
	}
	p.Reason = "QOS_QUEUE_SHARING_UNPROVEN"
	if !dedicatedQoSQueues(v, qosID, p.QueueUUIDs) {
		return
	}
	p.Reason, p.Dependency = "", Digest(deps)
	return
}

func qosEmptySet(value any) bool {
	values, ok := value.([]any)
	return ok && len(values) == 0
}

func qosLocalLabels(row Row) bool {
	labels, known := row.Configuration["external_ids"].(map[string]any)
	return known && !externalControl(labels)
}

func qosOtherOptions(options map[string]any, owned ...string) map[string]any {
	out := maps.Clone(options)
	for _, key := range owned {
		delete(out, key)
	}
	return out
}

func qosPersistentDependency(row Row) map[string]any {
	fields := maps.Clone(row.Configuration)
	if labels, ok := fields["external_ids"].(map[string]any); ok {
		fields["external_ids"] = qosOtherOptions(labels, candidate.QoSMarker)
	}
	delete(fields, "error")
	return fields
}

func qosRateNumber(v any) uint64 {
	text, _ := v.(string)
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != text {
		return 0
	}
	return n
}

func dedicatedQoSQueues(v *view, target string, queues map[uint32]string) bool {
	owned := make(map[string]bool, len(queues))
	for _, id := range queues {
		if owned[id] {
			return false
		}
		owned[id] = true
	}
	for id, row := range v.observation.Rows["QoS"] {
		if id == target {
			continue
		}
		mapping, known := row.Values["queues"].(map[string]any)
		if !known {
			return false
		}
		for _, value := range mapping {
			uuid, ok := value.(string)
			if !ok || owned[uuid] {
				return false
			}
		}
	}
	return true
}

func qosEditStatus(v *view, qos Binding, fresh string, allow map[string]string, ports qosBindings, c authn.Claims) map[string]any {
	reason := "EXCLUSIVE_QOS_AUTHORITY_REQUIRED"
	switch {
	case !slices.Contains(c.Capabilities, candidate.QoSCapability) || !slices.Contains(c.Capabilities, "workspace.write"):
		reason = "CAPABILITY_DENIED"
	case fresh != "fresh":
		reason = "OVS_ASSOCIATION_STALE"
	case !ports.complete:
		reason = "QOS_PORT_BINDING_UNKNOWN"
	case len(ports.ports[qos.UUID]) > 1:
		reason = "QOS_SHARED_PORT_BINDING"
	case len(ports.ports[qos.UUID]) == 0:
		reason = "QOS_PORT_UNBOUND"
	case len(ports.ports[qos.UUID]) == 1:
		p := qosRateTarget(v, ports.ports[qos.UUID][0].uuid, allow, ports)
		reason = p.Reason
		if reason == "" {
			return map[string]any{
				"available": true, "reason": "QOS_RATE_EDIT_AVAILABLE", "port": p.Binding,
				"revision": Digest([]any{p.Dependency, p.Rates}),
			}
		}
	}
	return map[string]any{"available": false, "reason": reason, "port": nil, "revision": nil}
}

func (s *Service) QoSRateEvidence(ctx context.Context, p candidate.QoSPort) (QoSRuntimeSample, error) {
	var sample QoSRuntimeSample
	current := func() bool {
		view, err := s.CandidateSnapshot(ctx, []candidate.Binding{p.Binding})
		return err == nil && Digest(view.QoSPorts[p.Binding.ManagementID]) == Digest(p)
	}
	if p.Reason != "" || !current() {
		return sample, apitypes.Fail(409, "QOS_BINDING_CHANGED")
	}
	s.mu.RLock()
	observer := s.qosObserver
	s.mu.RUnlock()
	if observer == nil {
		return sample, apitypes.Fail(503, "QOS_KERNEL_UNAVAILABLE")
	}
	request := QoSRuntimeRequest{Device: DeviceRequest{Name: p.Name, IfIndex: p.IfIndex}, Kind: strings.TrimPrefix(p.Rates.Type, "linux-"), QueueIDs: []uint32{0}, ExpectedMTU: p.MTU}
	for _, queue := range p.Rates.Queues {
		if queue.ID != 0 {
			request.QueueIDs = append(request.QueueIDs, queue.ID)
		}
	}
	sample = observer.ObserveQoS(ctx, request)
	age := s.now().Sub(sample.ObservedAt)
	if ctx.Err() != nil || !current() || sample.Scheduler != "observed" || sample.RootKind != request.Kind ||
		sample.Rates == nil || len(sample.ConfigurationDigest) != 64 || len(sample.StructureDigest) != 64 || age < 0 || age > time.Second {
		return sample, apitypes.Fail(409, "QOS_KERNEL_UNPROVEN")
	}
	return sample, nil
}

func QoSRatesMatch(installed *QoSInstalledRates, r candidate.QoSRates, mtu int) bool {
	if installed == nil || qosRateNumber(installed.MaximumBPS) != r.Maximum {
		return false
	}
	expected := make(map[uint32]candidate.QoSQueueRate, len(r.Queues)+1)
	floor := uint64(8)
	if r.Type == "linux-htb" {
		floor = uint64(mtu) * 8
	}
	expected[0] = candidate.QoSQueueRate{Minimum: floor, Maximum: r.Maximum}
	for _, queue := range r.Queues {
		expected[queue.ID] = queue
	}
	for _, q := range installed.Queues {
		value, ok := expected[q.QueueID]
		if !ok || qosRateNumber(q.MinimumBPS) != value.Minimum || qosRateNumber(q.MaximumBPS) != value.Maximum {
			return false
		}
		delete(expected, q.QueueID)
	}
	return len(expected) == 0
}
