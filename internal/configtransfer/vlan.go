// Package configtransfer translates typed configuration documents to a merge
// preview and Candidate intents. It has no storage, provider, or apply API.
package configtransfer

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const (
	FormatV1         = "ovs-webui.port-vlan.v1"
	MaxDocumentBytes = 128 << 10
)

type PortShape string

const (
	StandaloneSystem   PortShape = "standalone-system"
	StandaloneInternal PortShape = "standalone-internal"
)

type Document struct {
	Format       string         `json:"format"`
	Generation   string         `json:"instance_generation"`
	SchemaDigest string         `json:"schema_digest"`
	Ports        []PortDocument `json:"ports"`
}

type PortDocument struct {
	Binding candidate.Binding `json:"binding"`
	Member  candidate.Binding `json:"member"`
	Shape   PortShape         `json:"shape"`
	VLAN    *VLANFields       `json:"vlan"`
}

// Nil fields are absent in the document. For the native optional tag, []
// explicitly clears the tag and [id] assigns it. An empty trunks set retains
// its existing Candidate semantics, including all VLANs in trunk mode.
type VLANFields struct {
	Mode   *string `json:"vlan_mode,omitempty"`
	Tag    *[]int  `json:"tag,omitempty"`
	Trunks *[]int  `json:"trunks,omitempty"`
}

type Mapping struct {
	SourceID            string
	DestinationID       string
	DestinationMemberID string
}

type PortChange struct {
	Source, Destination             candidate.Binding
	SourceMember, DestinationMember candidate.Binding
	Shape                           PortShape
	Before, After                   candidate.VLAN
}

type MergePlan struct {
	SourceGeneration, SourceSchema       string
	Generation, Schema, Revision, Policy string
	Destinations                         []candidate.Binding
	Changes                              []PortChange
}

// CandidateIntents requires a fresh snapshot of the same destination scope as
// PlanMerge. This guards omitted fields from being copied out of a stale
// preview. The normal Candidate workflow still owns authorization and staging.
func (p MergePlan) CandidateIntents(current candidate.Snapshot) ([]candidate.Intent, error) {
	if !validSnapshot(current) || p.Generation != current.Generation || p.Schema != current.Schema ||
		p.Revision != current.Revision || p.Policy != current.Policy {
		return nil, apitypes.Fail(409, "CONFIG_SNAPSHOT_CHANGED")
	}
	result := make([]candidate.Intent, len(p.Changes))
	for i, change := range p.Changes {
		port, shape, err := exactPort(current, change.Destination.ManagementID)
		if err != nil || port.Binding != change.Destination || shape != change.Shape ||
			current.Topology.Nodes[port.Members[0]].Binding != change.DestinationMember {
			return nil, apitypes.Fail(409, "CONFIG_SNAPSHOT_CHANGED")
		}
		result[i] = vlanIntent(repository.NewID(), change.Destination, change.After)
	}
	return result, nil
}

// Export reads only explicitly selected Ports from an authorized, current
// Candidate snapshot. Order, native set ordering and transient revisions do
// not affect the document. Credentials, labels and native options have no slot.
func Export(current candidate.Snapshot, portIDs []string) ([]byte, error) {
	if !validSnapshot(current) {
		return nil, apitypes.Fail(409, "CONFIG_SNAPSHOT_UNPROVEN")
	}
	if len(portIDs) == 0 || len(portIDs) > candidate.MaxIntents {
		return nil, apitypes.Fail(422, "CONFIG_SCOPE_INVALID")
	}
	ids := slices.Clone(portIDs)
	slices.Sort(ids)
	intents := make([]candidate.Intent, 0, len(ids))
	document := Document{Format: FormatV1, Generation: current.Generation, SchemaDigest: current.Schema}
	for i, id := range ids {
		if i > 0 && ids[i-1] == id {
			return nil, apitypes.Fail(422, "CONFIG_SCOPE_INVALID")
		}
		port, shape, err := exactPort(current, id)
		if err != nil {
			return nil, err
		}
		document.Ports = append(document.Ports, PortDocument{Binding: port.Binding, Shape: shape,
			Member: current.Topology.Nodes[port.Members[0]].Binding})
		intents = append(intents, vlanIntent(id, port.Binding, port.VLAN))
	}
	prepared, err := checkDraft(current, intents)
	if err != nil {
		return nil, err
	}
	for i, item := range prepared {
		vlan := item.Value
		tag := []int{}
		if vlan.Tag != nil {
			tag = append(tag, *vlan.Tag)
		}
		document.Ports[i].VLAN = &VLANFields{Mode: vlan.Mode, Tag: &tag, Trunks: &vlan.Trunks}
	}
	if !validDocument(document) {
		return nil, apitypes.Fail(409, "CONFIG_SNAPSHOT_UNPROVEN")
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil || len(data)+1 > MaxDocumentBytes {
		return nil, apitypes.Fail(422, "CONFIG_DOCUMENT_LIMIT")
	}
	return append(data, '\n'), nil
}

func Decode(data []byte) (Document, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return Document{}, apitypes.Fail(422, "CONFIG_DOCUMENT_LIMIT")
	}
	raw, err := ipc.ParseObject(data)
	var document Document
	if err != nil || containsNull(raw) || ipc.DecodeStrict(data, &document) != nil || !validDocument(document) {
		return Document{}, apitypes.Fail(422, "CONFIG_DOCUMENT_INVALID")
	}
	return document, nil
}

// PlanMerge resolves destination IDs against the supplied current snapshot and
// composes only present fields. The plan contains no native write operations or
// instructions to reuse configuration authority from an imported identity.
func PlanMerge(data []byte, expectedGeneration string, current candidate.Snapshot, mappings []Mapping) (MergePlan, error) {
	return planVLANTransfer(data, expectedGeneration, current, mappings, false)
}

// PlanExactRestore requires complete source fields and an explicitly reviewed
// target member for every destination, including a same-generation restore.
func PlanExactRestore(data []byte, expectedGeneration string, current candidate.Snapshot, mappings []Mapping) (MergePlan, error) {
	return planVLANTransfer(data, expectedGeneration, current, mappings, true)
}

func planVLANTransfer(data []byte, expectedGeneration string, current candidate.Snapshot, mappings []Mapping, exact bool) (MergePlan, error) {
	document, err := Decode(data)
	if err != nil {
		return MergePlan{}, err
	}
	if current.Generation != expectedGeneration {
		return MergePlan{}, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	if !validSnapshot(current) {
		return MergePlan{}, apitypes.Fail(409, "CONFIG_SNAPSHOT_UNPROVEN")
	}
	targets, err := mappedPorts(document, current.Generation, mappings, exact)
	if err != nil {
		return MergePlan{}, err
	}
	plan := MergePlan{
		SourceGeneration: document.Generation, SourceSchema: document.SchemaDigest,
		Generation: current.Generation, Schema: current.Schema, Revision: current.Revision, Policy: current.Policy,
		Changes: []PortChange{},
	}
	slices.SortFunc(document.Ports, func(a, b PortDocument) int { return strings.Compare(a.Binding.ManagementID, b.Binding.ManagementID) })
	intents := make([]candidate.Intent, 0, len(document.Ports))
	var beforeImages []candidate.Intent
	nativeTargets := make(map[string]bool, len(document.Ports))
	for _, item := range document.Ports {
		target := targets[item.Binding.ManagementID]
		id := target.id
		port, shape, err := exactPort(current, id)
		if err != nil {
			return MergePlan{}, err
		}
		if shape != item.Shape {
			return MergePlan{}, apitypes.Fail(409, "CONFIG_PORT_SHAPE_CHANGED")
		}
		member := current.Topology.Nodes[port.Members[0]].Binding
		if target.memberID != "" && member.ManagementID != target.memberID {
			return MergePlan{}, apitypes.Fail(409, "CONFIG_DESTINATION_MEMBER_CHANGED")
		}
		if current.Generation == document.Generation && id == item.Binding.ManagementID {
			if port.Binding != item.Binding {
				return MergePlan{}, apitypes.Fail(409, "OBJECT_BINDING_CHANGED")
			}
			if !target.explicit && member != item.Member {
				return MergePlan{}, apitypes.Fail(409, "CONFIG_MAPPING_REQUIRED")
			}
		}
		if port.Binding.OVSUUID == member.OVSUUID || nativeTargets[port.Binding.OVSUUID] || nativeTargets[member.OVSUUID] {
			return MergePlan{}, apitypes.Fail(422, "CONFIG_MAPPING_INVALID")
		}
		nativeTargets[port.Binding.OVSUUID], nativeTargets[member.OVSUUID] = true, true
		plan.Destinations = append(plan.Destinations, port.Binding)
		base := port.VLAN
		if exact {
			if item.VLAN.Mode == nil || item.VLAN.Tag == nil || item.VLAN.Trunks == nil {
				return MergePlan{}, apitypes.Fail(422, "CONFIG_EXACT_FIELDS_REQUIRED")
			}
			beforeImages = append(beforeImages, vlanIntent(id, port.Binding, port.VLAN))
			base = candidate.VLAN{}
		}
		intents = append(intents, vlanIntent(id, port.Binding, mergeFields(base, *item.VLAN)))
	}
	if exact {
		if _, err := checkDraft(current, beforeImages); err != nil {
			return MergePlan{}, apitypes.Fail(409, "CONFIG_EXACT_BEFORE_UNSUPPORTED")
		}
	}
	prepared, err := checkDraft(current, intents)
	if err != nil {
		return MergePlan{}, err
	}
	for i, item := range prepared {
		if candidate.Digest(item.Before) == candidate.Digest(item.Value) {
			continue
		}
		source := document.Ports[i]
		member := current.Ports[item.Object.ManagementID].Members[0]
		plan.Changes = append(plan.Changes, PortChange{
			Source: source.Binding, Destination: item.Object, Shape: source.Shape,
			SourceMember: source.Member, DestinationMember: current.Topology.Nodes[member].Binding,
			Before: copyVLAN(item.Before), After: copyVLAN(item.Value),
		})
	}
	return plan, nil
}

type resolvedPort struct {
	id       string
	memberID string
	explicit bool
}

func mappedPorts(document Document, generation string, mappings []Mapping, exact bool) (map[string]resolvedPort, error) {
	if len(mappings) > len(document.Ports) {
		return nil, apitypes.Fail(422, "CONFIG_MAPPING_INVALID")
	}
	sources := make(map[string]bool, len(document.Ports))
	for _, item := range document.Ports {
		sources[item.Binding.ManagementID] = true
	}
	targets := make(map[string]resolvedPort, len(document.Ports))
	for _, mapping := range mappings {
		if !sources[mapping.SourceID] || !apitypes.ManagementID(mapping.DestinationID) || targets[mapping.SourceID].id != "" ||
			(mapping.DestinationMemberID != "" && !apitypes.ManagementID(mapping.DestinationMemberID)) {
			return nil, apitypes.Fail(422, "CONFIG_MAPPING_INVALID")
		}
		if exact && mapping.DestinationMemberID == "" {
			return nil, apitypes.Fail(422, "CONFIG_MAPPING_REQUIRED")
		}
		targets[mapping.SourceID] = resolvedPort{id: mapping.DestinationID, memberID: mapping.DestinationMemberID, explicit: true}
	}
	seen := make(map[string]bool, len(targets))
	for _, item := range document.Ports {
		source := item.Binding.ManagementID
		target := targets[source]
		if target.id == "" {
			if exact || generation != document.Generation {
				return nil, apitypes.Fail(409, "CONFIG_MAPPING_REQUIRED")
			}
			target = resolvedPort{id: source}
			targets[source] = target
		}
		if seen[target.id] {
			return nil, apitypes.Fail(422, "CONFIG_MAPPING_INVALID")
		}
		seen[target.id] = true
	}
	return targets, nil
}

func exactPort(current candidate.Snapshot, id string) (candidate.Port, PortShape, error) {
	port, ok := current.Ports[id]
	if !ok || current.Topology.Retired[id] || !validBinding(port.Binding, current.Generation, "Port") || port.Binding.ManagementID != id ||
		!port.Known || !port.SchemaSupported || port.Authority != "local-managed" || !digest(port.Dependency) ||
		len(port.Members) != 1 || port.VLAN.Mode == nil ||
		!supportedMode(*port.VLAN.Mode) || len(port.VLAN.CVLANs) != 0 {
		return candidate.Port{}, "", apitypes.Fail(409, "CONFIG_PORT_UNPROVEN")
	}
	node := current.Topology.Nodes[id]
	if node.Binding != port.Binding || len(node.Links) != 1 {
		return candidate.Port{}, "", apitypes.Fail(409, "CONFIG_PORT_SHAPE_UNPROVEN")
	}
	link := node.Links[0]
	member := current.Topology.Nodes[port.Members[0]]
	if link != member.Binding || link.ManagementID != port.Members[0] || current.Topology.Retired[link.ManagementID] ||
		!validBinding(link, current.Generation, "Interface") {
		return candidate.Port{}, "", apitypes.Fail(409, "CONFIG_PORT_SHAPE_UNPROVEN")
	}
	if (member.Type != "internal" && port.LocalPort) ||
		(member.Type == "internal" && !standaloneInternalPort(current, node)) {
		return candidate.Port{}, "", apitypes.Fail(409, "CONFIG_PORT_UNPROVEN")
	}
	switch member.Type {
	case "internal":
		return port, StandaloneInternal, nil
	case "", "system":
		if port.MemberKindsSupported {
			return port, StandaloneSystem, nil
		}
	}
	return candidate.Port{}, "", apitypes.Fail(409, "CONFIG_PORT_SHAPE_UNPROVEN")
}

// The Bond projection protects all internal Interfaces. For a VLAN transfer,
// distinguish a separate internal Port from its Bridge's reserved local Port.
func standaloneInternalPort(current candidate.Snapshot, port candidate.TopologyNode) bool {
	if port.Name == "" {
		return false
	}
	parents := 0
	for id, bridge := range current.Topology.Nodes {
		if bridge.Binding.Table != "Bridge" || !slices.Contains(bridge.Links, port.Binding) {
			continue
		}
		if bridge.Name == "" || bridge.Name == port.Name || current.Topology.Retired[id] ||
			bridge.Binding.ManagementID != id || !validBinding(bridge.Binding, current.Generation, "Bridge") {
			return false
		}
		parents++
	}
	return parents == 1
}

func checkDraft(current candidate.Snapshot, intents []candidate.Intent) ([]candidate.StoredIntent, error) {
	draft, err := candidate.Prepare(candidate.Envelope{}, candidate.Command{Operation: "stage", Intents: intents}, current)
	if err != nil {
		return nil, err
	}
	checks, _ := candidate.Checks(draft.Candidate, current)
	for _, check := range checks {
		if check.State != "allowed" {
			return nil, apitypes.Fail(409, check.Code)
		}
	}
	return draft.Candidate.Intents, nil
}

func vlanIntent(id string, binding candidate.Binding, value candidate.VLAN) candidate.Intent {
	return candidate.Intent{ID: id, Operation: "port.vlan.set", Object: binding, Value: copyVLAN(value)}
}

func mergeFields(current candidate.VLAN, fields VLANFields) candidate.VLAN {
	result := copyVLAN(current)
	if fields.Mode != nil {
		result.Mode = copyValue(fields.Mode)
	}
	if fields.Tag != nil {
		result.Tag = nil
		if len(*fields.Tag) == 1 {
			value := (*fields.Tag)[0]
			result.Tag = &value
		}
	}
	if fields.Trunks != nil {
		result.Trunks = slices.Clone(*fields.Trunks)
	}
	return result
}

func copyVLAN(value candidate.VLAN) candidate.VLAN {
	value.Mode, value.Tag = copyValue(value.Mode), copyValue(value.Tag)
	value.Trunks, value.CVLANs = slices.Clone(value.Trunks), slices.Clone(value.CVLANs)
	return value
}

func copyValue[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

func validSnapshot(s candidate.Snapshot) bool {
	return apitypes.ManagementID(s.Generation) && digest(s.Schema) && digest(s.Revision) && digest(s.Policy)
}

func validBinding(b candidate.Binding, generation, table string) bool {
	return b.Generation == generation && b.Table == table && apitypes.ManagementID(b.ManagementID) && apitypes.UUID(b.OVSUUID)
}

func digest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func validDocument(d Document) bool {
	if d.Format != FormatV1 || !apitypes.ManagementID(d.Generation) || !digest(d.SchemaDigest) ||
		len(d.Ports) == 0 || len(d.Ports) > candidate.MaxIntents {
		return false
	}
	ids, native := map[string]bool{}, map[string]bool{}
	for _, item := range d.Ports {
		if !validBinding(item.Binding, d.Generation, "Port") || !validBinding(item.Member, d.Generation, "Interface") ||
			(item.Shape != StandaloneInternal && item.Shape != StandaloneSystem) || item.VLAN == nil || !validFields(*item.VLAN) {
			return false
		}
		for _, b := range []candidate.Binding{item.Binding, item.Member} {
			if ids[b.ManagementID] || native[b.OVSUUID] {
				return false
			}
			ids[b.ManagementID], native[b.OVSUUID] = true, true
		}
	}
	return true
}

func supportedMode(value string) bool {
	switch value {
	case "access", "trunk", "native-tagged", "native-untagged":
		return true
	}
	return false
}

func validFields(f VLANFields) bool {
	if f.Mode != nil && !supportedMode(*f.Mode) || f.Tag != nil && (len(*f.Tag) > 1 || !validVLANSet(*f.Tag)) {
		return false
	}
	return f.Trunks == nil || validVLANSet(*f.Trunks)
}

func validVLANSet(values []int) bool {
	if len(values) > 4094 {
		return false
	}
	var seen [4095]bool
	for _, id := range values {
		if id < 1 || id > 4094 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func containsNull(data any) bool {
	switch value := data.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range value {
			if containsNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if containsNull(item) {
				return true
			}
		}
	}
	return false
}
