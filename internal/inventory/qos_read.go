package inventory

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

type qosRate struct {
	State string  `json:"state"`
	BPS   *string `json:"bps"`
}

type qosQueue struct {
	ID        uint32       `json:"queue_id"`
	Ref       apitypes.Ref `json:"queue_ref"`
	MinRate   qosRate      `json:"min_rate"`
	MaxRate   qosRate      `json:"max_rate"`
	DSCP      *uint8       `json:"dscp"`
	DSCPState string       `json:"dscp_state"`
}

type qosPort struct {
	Ref  apitypes.Ref `json:"port_ref"`
	Name string       `json:"name"`
}

type qosBindings struct {
	ports    map[string][]qosPort
	complete bool
}

func qosPortTarget(row Row) (string, bool) {
	values, ok := row.Configuration["qos"].([]any)
	if !ok || len(values) > 1 {
		return "", false
	}
	if len(values) == 0 {
		return "", true
	}
	id, ok := values[0].(string)
	return id, ok && apitypes.UUID(id)
}

func indexQoSPorts(v *view) qosBindings {
	out := qosBindings{ports: map[string][]qosPort{}, complete: true}
	for _, row := range v.observation.Rows["Port"] {
		id, known := qosPortTarget(row)
		if !known {
			out.complete = false
			continue
		}
		if id == "" {
			continue
		}
		binding := v.decision.Bindings[Key("Port", row.UUID)]
		if binding.State != "active" {
			out.complete = false
			continue
		}
		out.ports[id] = append(out.ports[id], qosPort{Ref: apitypes.Ref{Kind: "port", ID: binding.ManagementID}, Name: textValue(row.Values["name"])})
	}
	return out
}

func portQoSBinding(v *view, row Row, config bool) map[string]any {
	out := map[string]any{"state": "unknown", "qos_ref": nil}
	switch {
	case !config:
		out["state"] = "withheld"
	case !v.observation.Schema.QoSConfiguration:
		out["state"] = "unsupported"
	default:
		id, known := qosPortTarget(row)
		if !known {
			break
		}
		if id == "" {
			out["state"] = "unbound"
		} else if _, exists := v.observation.Rows["QoS"][id]; exists && v.decision.Bindings[Key("QoS", id)].State == "active" {
			out["state"], out["qos_ref"] = "bound", ref(v, "QoS", id)
		}
	}
	return out
}

func readQoSRate(row Row, key string, applicable bool) qosRate {
	if !applicable {
		return qosRate{State: "not-applicable"}
	}
	options, ok := row.Values["other_config"].(map[string]any)
	if !ok {
		return qosRate{State: "unknown"}
	}
	value, present := options[key]
	if !present {
		return qosRate{State: "unset"}
	}
	rate, ok := value.(string)
	number, err := strconv.ParseUint(rate, 10, 64)
	if !ok || err != nil || number == 0 || strings.Trim(rate, "0123456789") != "" {
		return qosRate{State: "unknown"}
	}
	return qosRate{State: "configured", BPS: &rate}
}

func readQoSQueues(v *view, row Row, rates, detail bool) ([]qosQueue, int, bool) {
	mapping, ok := row.Values["queues"].(map[string]any)
	if !ok {
		return nil, 0, false
	}
	var queues []qosQueue
	if detail {
		queues = make([]qosQueue, 0, len(mapping))
	}
	for key, value := range mapping {
		id, err := strconv.ParseUint(key, 10, 32)
		uuid, uuidOK := value.(string)
		q, exists := v.observation.Rows["Queue"][uuid]
		b := v.decision.Bindings[Key("Queue", uuid)]
		if err != nil || !uuidOK || !exists || b.State != "active" {
			return nil, 0, false
		}
		if !detail {
			continue
		}
		queue := qosQueue{
			ID: uint32(id), Ref: apitypes.Ref{Kind: "queue", ID: b.ManagementID},
			MinRate: readQoSRate(q, "min-rate", rates), MaxRate: readQoSRate(q, "max-rate", rates),
			DSCPState: "unknown",
		}
		if dscp, ok := q.Values["dscp"].([]any); ok && len(dscp) == 0 {
			queue.DSCPState = "unset"
		} else if raw, ok := one(q.Values["dscp"]).(string); ok {
			if n, err := strconv.ParseUint(raw, 10, 6); err == nil {
				code := uint8(n)
				queue.DSCP, queue.DSCPState = &code, "configured"
			}
		}
		queues = append(queues, queue)
	}
	slices.SortFunc(queues, func(a, b qosQueue) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return queues, len(mapping), true
}

func qosResource(v *view, b Binding, fresh string, bindings qosBindings, detail bool) (map[string]any, error) {
	row, exists := v.observation.Rows["QoS"][b.UUID]
	if !exists || b.State != "active" {
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	out := base(b.ManagementID, "qos", fresh, v)
	out["source"] = source(v, fresh, "ovsdb-configuration")
	out["management_id"], out["ovs_uuid"], out["instance_generation"] = b.ManagementID, b.UUID, v.decision.Generation
	typ, known := row.Values["type"].(string)
	var discipline any
	if known && len(typ) <= 128 && !strings.ContainsAny(typ, "\x00\r\n") {
		discipline = typ
	}
	rates := typ == "linux-htb" || typ == "linux-hfsc"
	queues, count, complete := readQoSQueues(v, row, rates, detail)
	var queueCount any
	if complete {
		queueCount = count
	}
	ports := bindings.ports[b.UUID]
	status := "unknown"
	var portCount any
	if bindings.complete {
		portCount = len(ports)
		switch len(ports) {
		case 0:
			status = "unbound"
		case 1:
			status = "bound"
		default:
			status = "shared"
		}
	}
	var portDetails []qosPort
	if detail {
		portDetails = slices.Clone(ports)
		if portDetails == nil {
			portDetails = []qosPort{}
		}
		slices.SortFunc(portDetails, func(a, b qosPort) int { return naturalNameCompare(a.Name, b.Name) })
		if len(portDetails) > 32 {
			portDetails = portDetails[:32]
		}
	}
	out["native_qos"] = map[string]any{
		"discipline": discipline, "max_rate": readQoSRate(row, "max-rate", rates),
		"binding_state": status, "port_count": portCount, "ports": portDetails,
		"ports_truncated": len(ports) > 32, "queue_count": queueCount, "queues": queues,
	}
	return out, nil
}
