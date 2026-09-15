package inventory

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

type view struct {
	observation Observation
	decision    Decision
	id          string
	digest      string
	sequence    uint64
}
type Service struct {
	registry Registry
	mu       sync.RWMutex
	current  *view
	failure  string
	id       string
	key      []byte
	now      func() time.Time
}

func New(r Registry) *Service {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("system randomness unavailable")
	}
	return &Service{registry: r, id: repository.NewID(), key: key, now: time.Now, failure: "OVSDB_RESYNCING"}
}
func (s *Service) Previous(ctx context.Context) (Evidence, error) {
	d, err := s.registry.Previous(ctx)
	return d.Evidence, err
}
func (s *Service) Publish(ctx context.Context, o Observation) error {
	d, err := s.registry.Reconcile(ctx, o)
	if err != nil {
		s.Unavailable("INVENTORY_STORAGE_UNAVAILABLE")
		return err
	}
	s.install(o, d)
	return nil
}
func (s *Service) install(o Observation, d Decision) {
	digest := Digest(struct {
		Rows                      Rows
		Schema, Generation, State string
	}{o.Rows, o.Schema.Digest, d.Generation, d.State})
	s.mu.Lock()
	defer s.mu.Unlock()
	v := &view{observation: o, decision: d, id: repository.NewID(), digest: digest, sequence: 1}
	if s.current != nil {
		v.sequence = s.current.sequence + 1
		if s.current.digest == digest {
			v.id = s.current.id
			v.sequence = s.current.sequence
		}
	}
	s.current = v
	s.failure = ""
}
func (s *Service) Unavailable(code string) { s.mu.Lock(); defer s.mu.Unlock(); s.failure = code }
func (s *Service) Accept(ctx context.Context, digest, reason string) error {
	s.mu.RLock()
	v := s.current
	failure := s.failure
	s.mu.RUnlock()
	if v == nil || failure != "" || s.now().Sub(v.observation.Evidence.ObservedAt) > FreshFor {
		return apitypes.Fail(503, "INVENTORY_NOT_FRESH")
	}
	d, err := s.registry.Accept(ctx, v.observation, digest, reason)
	if err != nil {
		return err
	}
	s.install(v.observation, d)
	return nil
}
func (s *Service) PendingDigest() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return ""
	}
	return s.current.decision.PendingDigest
}
func (s *Service) Observed() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.current != nil }

type cursor struct {
	Principal, Permission, Operation, Filter, Generation, Snapshot, After string
	Limit                                                                 int
	Expires                                                               int64
}

func (s *Service) Read(_ context.Context, op string, path map[string]string, q url.Values, c authn.Claims) (any, error) {
	if !Operation(op) {
		return nil, apitypes.Fail(403, "OPERATION_DENIED")
	}
	if !slices.Contains(c.Capabilities, "inventory.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	s.mu.RLock()
	v, failure := s.current, s.failure
	s.mu.RUnlock()
	now := s.now()
	fresh := "unknown"
	availability := "unavailable"
	reason := failure
	if v != nil {
		fresh = "fresh"
		availability = "complete"
		age := now.Sub(v.observation.Evidence.ObservedAt)
		if failure != "" || age < 0 || age > FreshFor {
			fresh = "stale"
			availability = "degraded"
			if reason == "" {
				reason = "OVSDB_OBSERVATION_EXPIRED"
			}
		}
		if v.decision.State != "confirmed" {
			availability = "degraded"
			reason = v.decision.Reason
		}
	}
	if op == "readInventory" {
		return s.status(v, fresh, availability, reason), nil
	}
	if v == nil {
		return nil, apitypes.Fail(503, "OVSDB_RESYNCING")
	}
	if op != "readInventorySchema" && v.decision.State != "confirmed" {
		return nil, apitypes.Fail(503, "INSTANCE_RECONCILIATION_REQUIRED")
	}
	allowedConfig := slices.Contains(c.Capabilities, "configuration.read")
	if len(path) > 0 {
		kind := map[string]string{"readPort": "port", "readBridge": "bridge", "readInterface": "interface", "readBond": "bond"}[op]
		for _, b := range v.decision.Bindings {
			for _, id := range path {
				if b.ManagementID == id && kindFor(b.Table) == tableKind(kind) {
					item, err := resource(v, b, fresh, allowedConfig, kind)
					if err != nil {
						return nil, err
					}
					return item, nil
				}
			}
		}
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	limit := 100
	if q.Get("limit") != "" {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			return nil, apitypes.Fail(422, "INVALID_LIMIT")
		}
	}
	filter := q.Get("filter")
	if len(filter) > 1024 {
		return nil, apitypes.Fail(422, "INVALID_FILTER")
	}
	cur := cursor{Principal: c.PrincipalID, Permission: c.Revision, Operation: op, Filter: filter, Generation: v.decision.Generation, Snapshot: v.id, Limit: limit, Expires: now.Add(30 * time.Second).UnixMilli()}
	if token := q.Get("cursor"); token != "" {
		if len(token) > 4096 {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		b, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		b, err = authn.Unseal(s.key, b, []byte("inventory-page-v1"))
		if err != nil {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		var old cursor
		if json.Unmarshal(b, &old) != nil || old.Principal != cur.Principal || old.Permission != cur.Permission || old.Operation != cur.Operation || old.Filter != cur.Filter || old.Generation != cur.Generation || old.Snapshot != cur.Snapshot || old.Limit != cur.Limit || old.Expires <= now.UnixMilli() {
			return nil, apitypes.Fail(410, "CURSOR_EXPIRED")
		}
		cur = old
	}
	items := []map[string]any{}
	kind := map[string]string{"listPorts": "port", "listBridges": "bridge", "listInterfaces": "interface", "listBonds": "bond"}[op]
	ids := []string{}
	bindings := map[string]Binding{}
	if op == "readInventorySchema" {
		for _, table := range v.observation.Schema.Tables {
			if strings.Contains(strings.ToLower(table.Name), strings.ToLower(filter)) {
				ids = append(ids, table.Name)
			}
		}
	} else {
		for _, b := range v.decision.Bindings {
			if kindFor(b.Table) != tableKind(kind) {
				continue
			}
			row := v.observation.Rows[b.Table][b.UUID]
			if kind == "bond" && len(refs(row.Values["interfaces"])) < 2 {
				continue
			}
			if !strings.Contains(strings.ToLower(textValue(row.Values["name"])), strings.ToLower(filter)) {
				continue
			}
			ids = append(ids, b.ManagementID)
			bindings[b.ManagementID] = b
		}
	}
	sort.Strings(ids)
	size := 0
	more := false
	for _, id := range ids {
		if id <= cur.After {
			continue
		}
		if len(items) >= limit {
			more = true
			break
		}
		var item map[string]any
		var err error
		if op == "readInventorySchema" {
			for _, table := range v.observation.Schema.Tables {
				if table.Name == id {
					item = base(schemaID(v.observation.Schema.Digest, table.Name), "schema_table", fresh, v)
					item["name"] = table.Name
					item["columns"] = table.Columns
					item["indexes"] = table.Indexes
					item["is_root"] = table.Root
					item["schema_digest"] = v.observation.Schema.Digest
					break
				}
			}
		} else {
			item, err = resource(v, bindings[id], fresh, allowedConfig, kind)
			if err != nil {
				return nil, err
			}
		}
		b, _ := json.Marshal(item)
		if len(b) > 40<<10 {
			return nil, apitypes.Fail(503, "INVENTORY_REPRESENTATION_BUDGET")
		}
		if size+len(b) > 40<<10 {
			more = true
			break
		}
		size += len(b)
		items = append(items, item)
		cur.After = id
	}
	var next any
	if more {
		b, _ := json.Marshal(cur)
		sealed, err := authn.Seal(s.key, b, []byte("inventory-page-v1"))
		if err != nil {
			return nil, err
		}
		next = base64.RawURLEncoding.EncodeToString(sealed)
	}
	return map[string]any{"snapshot_id": v.id, "instance_generation": v.decision.Generation, "source": source(v, fresh, "ovsdb"), "availability": availability, "reason": reason, "items": items, "next_cursor": next, "truncated": more, "coverage": coverage(v)}, nil
}
func (s *Service) status(v *view, fresh, availability, reason string) map[string]any {
	out := base(s.id, "inventory", fresh, v)
	out["availability"] = availability
	out["reason"] = reason
	out["configuration_ready"] = false
	out["instance_generation"] = nil
	out["reconciliation_state"] = "unknown"
	out["reviewed_evidence_digest"] = nil
	out["coverage"] = map[string]any{"state": "unknown", "max_rows": MaxRows}
	out["schema_digest"] = nil
	if v != nil {
		out["snapshot_id"] = v.id
		out["instance_generation"] = v.decision.Generation
		out["reconciliation_state"] = v.decision.State
		out["lifecycle_reason"] = v.decision.Reason
		out["reviewed_evidence_digest"] = v.decision.PendingDigest
		out["coverage"] = coverage(v)
		out["schema_digest"] = v.observation.Schema.Digest
		out["continuity_evidence"] = map[string]any{"root_uuid": v.observation.Evidence.Root, "anchor_count": len(v.observation.Evidence.Anchors), "file_available": v.observation.Evidence.File.Available, "server_file_bound": v.observation.Evidence.File.ServerHasFile, "file_binding_method": v.observation.Evidence.File.ServerBinding, "file_prefix_continues": v.observation.Evidence.File.PriorMatches, "monitor_continuous": v.observation.Evidence.Continuous}
		for _, root := range v.observation.Rows["Open_vSwitch"] {
			out["ovs_progress"] = map[string]any{"next_cfg": one(root.Values["next_cfg"]), "cur_cfg": one(root.Values["cur_cfg"]), "source": source(v, fresh, "ovsdb-progress")}
		}
	}
	return out
}
func coverage(v *view) map[string]any {
	counts := map[string]int{}
	for table, rows := range v.observation.Rows {
		counts[table] = len(rows)
	}
	return map[string]any{"state": "complete-for-selected-columns", "row_counts": counts, "max_rows": MaxRows, "max_bytes": MaxSnapshotBytes, "includes_linux": false, "option_keys": "peer,remote_ip,local_ip,dst_port,key; remaining keys withheld", "schema_columns": "see /api/v1/inventory/schema"}
}
func source(v *view, fresh, authority string) map[string]any {
	var at any
	confidence := "unknown"
	if v != nil {
		at = v.observation.Evidence.ObservedAt
		confidence = "proven"
		if v.decision.State != "confirmed" {
			confidence = "partial"
		}
	}
	return map[string]any{"provider_id": "ovsdb", "authority": authority, "observed_at": at, "freshness": fresh, "confidence": confidence}
}
func base(id, kind, fresh string, v *view) map[string]any {
	seq := "0"
	if v != nil {
		seq = strconv.FormatUint(v.sequence, 10)
	}
	return map[string]any{"id": id, "resource_kind": kind, "state": fresh, "sequence": seq, "source": source(v, fresh, "ovsdb"), "allowed_actions": []string{}}
}
func kindFor(t string) string {
	return map[string]string{"Bridge": "bridge", "Port": "port", "Interface": "interface"}[t]
}
func tableKind(k string) string {
	if k == "bond" {
		return "port"
	}
	return k
}
func schemaID(digest, name string) string {
	h := Digest([]string{digest, name})
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-8" + h[17:20] + "-" + h[20:32]
}
func refs(v any) []string {
	out := []string{}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	if a, ok := v.([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func one(v any) any {
	if a, ok := v.([]any); ok {
		if len(a) == 1 {
			return a[0]
		}
		return nil
	}
	return v
}
func textValue(v any) string { s, _ := one(v).(string); return s }
func nativeType(row Row, column string, config bool) string {
	value, known := row.Values[column].(string)
	if !known || !config {
		return "unknown"
	}
	if value == "" {
		return "default"
	}
	return value
}
func ref(v *view, table, uuid string) map[string]any {
	b := v.decision.Bindings[Key(table, uuid)]
	return map[string]any{"kind": kindFor(table), "id": b.ManagementID}
}
func parent(v *view, table, column, uuid string) (Row, bool) {
	for _, row := range v.observation.Rows[table] {
		if slices.Contains(refs(row.Values[column]), uuid) {
			return row, true
		}
	}
	return Row{}, false
}

func operational(table, column string) bool {
	return table == "Interface" && slices.Contains([]string{"link_state", "admin_state", "ofport", "ifindex", "mtu", "link_speed", "duplex", "error"}, column)
}
func resource(v *view, b Binding, fresh string, config bool, kind string) (map[string]any, error) {
	row, exists := v.observation.Rows[b.Table][b.UUID]
	if !exists {
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	if kind == "bond" && len(refs(row.Values["interfaces"])) < 2 {
		return nil, apitypes.Fail(404, "NOT_FOUND")
	}
	out := base(b.ManagementID, kind, fresh, v)
	out["management_id"] = b.ManagementID
	out["ovs_uuid"] = b.UUID
	out["instance_generation"] = v.decision.Generation
	out["name"] = row.Values["name"]
	values := map[string]any{}
	fields := map[string]any{}
	for _, table := range v.observation.Schema.Tables {
		if table.Name != b.Table {
			continue
		}
		for _, col := range table.Columns {
			if !col.Monitored {
				continue
			}
			value, known := row.Values[col.Name]
			available := "known"
			authority := "ovsdb-configuration"
			if operational(b.Table, col.Name) {
				authority = "ovs-vswitchd-observation"
			}
			// Structural identity is inventory scope. Configuration values additionally
			// require configuration.read even when state.read has already been checked.
			restricted := !config && !operational(b.Table, col.Name) && !slices.Contains([]string{"name", "ports", "interfaces"}, col.Name)
			if !known || restricted {
				value = nil
				available = "unknown"
			}
			if restricted {
				available = "withheld"
			}
			fields[col.Name] = map[string]any{"value": value, "availability": available, "source": source(v, fresh, authority), "schema_mutable": col.Mutable, "ownership": "unknown", "editable": false}
			if !operational(b.Table, col.Name) {
				values[col.Name] = value
			}
		}
	}
	out["fields"] = fields
	out["config_revision"] = Digest(values)
	out["ownership"] = "unknown"
	out["allowed_operations"] = []string{}
	switch b.Table {
	case "Bridge":
		ports := []any{}
		for _, id := range refs(row.Values["ports"]) {
			ports = append(ports, ref(v, "Port", id))
		}
		out["port_refs"] = ports
		out["datapath_type"] = nativeType(row, "datapath_type", config)
	case "Port":
		bridge, ok := parent(v, "Bridge", "ports", b.UUID)
		if !ok {
			return nil, apitypes.Fail(503, "INVENTORY_RELATION_UNKNOWN")
		}
		out["bridge_ref"] = ref(v, "Bridge", bridge.UUID)
		members := []any{}
		for _, id := range refs(row.Values["interfaces"]) {
			members = append(members, ref(v, "Interface", id))
		}
		out["interface_refs"] = members
		out["kind"] = "single"
		if len(members) > 1 {
			out["kind"] = "bond"
		}
		out["local_port"] = false
		for _, id := range refs(row.Values["interfaces"]) {
			iface := v.observation.Rows["Interface"][id]
			if row.Values["name"] == bridge.Values["name"] && iface.Values["name"] == bridge.Values["name"] {
				typ, known := iface.Values["type"].(string)
				if !known {
					out["local_port"] = nil
				} else if typ == "internal" {
					out["local_port"] = true
					break
				}
			}
		}
		var native any
		availability, reason := "unknown", any("configuration-withheld-or-column-unavailable")
		if config {
			mode, modeOK := row.Values["vlan_mode"]
			tag, tagOK := row.Values["tag"]
			trunks, trunksOK := row.Values["trunks"]
			cvlans, cvlansOK := row.Values["cvlans"]
			if modeOK && tagOK && trunksOK && cvlansOK {
				tags, err := vlans(tag)
				if err != nil {
					return nil, err
				}
				tt, err := vlans(trunks)
				if err != nil {
					return nil, err
				}
				cc, err := vlans(cvlans)
				if err != nil {
					return nil, err
				}
				var t any
				if len(tags) > 0 {
					t = tags[0]
				}
				native = map[string]any{"vlan_mode": one(mode), "tag": t, "trunks": tt, "cvlans": cc}
				availability, reason = "known", nil
			}
		}
		out["vlan"] = map[string]any{"availability": availability, "native": native, "source": source(v, fresh, "ovsdb-configuration"), "reason": reason}
		out["ovs_link_state"] = map[string]any{"value": nil, "source": source(v, "unknown", "member-interface-observation")}
		out["linux_carrier"] = map[string]any{"value": nil, "source": map[string]any{"provider_id": "linux", "authority": "linux-carrier", "observed_at": nil, "freshness": "unavailable", "confidence": "unknown"}}
		if kind == "bond" {
			out["member_refs"] = members
			out["lacp"] = "unknown"
			out["bond_mode"] = "unknown"
			if config {
				for _, col := range []string{"lacp", "bond_mode"} {
					if value := textValue(row.Values[col]); value != "" {
						out[col] = value
					}
				}
			}
		}
	case "Interface":
		p, ok := parent(v, "Port", "interfaces", b.UUID)
		if !ok {
			return nil, apitypes.Fail(503, "INVENTORY_RELATION_UNKNOWN")
		}
		out["port_ref"] = ref(v, "Port", p.UUID)
		out["interface_type"] = nativeType(row, "type", config)
		out["internal"] = nil
		if typ, known := row.Values["type"].(string); known {
			out["internal"] = typ == "internal"
		}
		out["options"] = map[string]any{}
		if config {
			if options, ok := row.Values["options"].(map[string]any); ok {
				out["options"] = options
			}
		}
	}
	return out, nil
}
func vlans(v any) ([]int, error) {
	out := []int{}
	for _, s := range refs(v) {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 4095 {
			return nil, apitypes.Fail(503, "OVSDB_VLAN_VALUE_INVALID")
		}
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}
