package configtransfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
)

func fixtureID(value int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012x", value) }

func observedVLAN(mode string, tag *int, trunks ...int) candidate.VLAN {
	return candidate.VLAN{Mode: &mode, Tag: tag, Trunks: trunks, CVLANs: []int{}}
}

func fixtureSnapshot(gen int) candidate.Snapshot {
	return candidate.Snapshot{
		Generation: fixtureID(gen), Schema: candidate.Digest("source schema"),
		Revision: candidate.Digest("source revision"), Policy: candidate.Digest("source policy"),
		Ports: map[string]candidate.Port{},
		Topology: candidate.TopologySnapshot{
			Nodes:          map[string]candidate.TopologyNode{},
			Configurations: map[string]map[string]any{},
		},
	}
}

func fixturePort(s *candidate.Snapshot, value int, typ string, vlan candidate.VLAN) candidate.Port {
	id, memberID := fixtureID(value), fixtureID(value+1000)
	b := candidate.Binding{ManagementID: id, OVSUUID: fixtureID(value + 2000), Table: "Port", Generation: s.Generation}
	child := candidate.Binding{ManagementID: memberID, OVSUUID: fixtureID(value + 3000), Table: "Interface", Generation: s.Generation}
	bridge := candidate.Binding{ManagementID: fixtureID(value + 4000), OVSUUID: fixtureID(value + 5000), Table: "Bridge", Generation: s.Generation}
	p := candidate.Port{
		Binding: b, VLAN: vlan, Known: true, SchemaSupported: true, Authority: "local-managed",
		Dependency: candidate.Digest("port dependency"), Members: []string{memberID},
		MemberKindsSupported: typ == "system" || typ == "",
		LocalPort:            typ == "internal",
		Modes:                []string{"access", "trunk", "native-tagged", "native-untagged"},
	}
	s.Ports[id] = p
	s.Topology.Nodes[id] = candidate.TopologyNode{Binding: b, Name: "synthetic-private-label", Links: []candidate.Binding{child}}
	s.Topology.Nodes[memberID] = candidate.TopologyNode{Binding: child, Name: "synthetic-shared-name", Type: typ}
	s.Topology.Nodes[bridge.ManagementID] = candidate.TopologyNode{Binding: bridge, Name: "synthetic-bridge", Links: []candidate.Binding{b}}
	s.Topology.Configurations[id] = map[string]any{"private_key": "synthetic-credential-marker"}
	return p
}

func TestInternalPortVLANTransferKeepsBridgeLocalPortProtected(t *testing.T) {
	const selected = 20
	s := fixtureSnapshot(1)
	tag, change := 9, []int{12}
	port := fixturePort(&s, selected, "internal", observedVLAN("access", &tag))
	id, bridgeID := port.Binding.ManagementID, fixtureID(selected+4000)
	document := marshalDocument(t, fixtureDocument(s, port, StandaloneInternal, VLANFields{Tag: &change}))
	if _, err := Export(s, []string{id}); err != nil {
		t.Fatalf("standalone internal Port export failed: %v", err)
	}
	if planned, err := PlanMerge(document, s.Generation, s, nil); err != nil || len(planned.Changes) != 1 {
		t.Fatalf("standalone internal Port did not reach Candidate review: %v", err)
	}
	parent := s.Topology.Nodes[bridgeID]
	parent.Name = s.Topology.Nodes[id].Name
	s.Topology.Nodes[bridgeID] = parent
	_, err := Export(s, []string{id})
	expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
	_, err = PlanMerge(document, s.Generation, s, nil)
	expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
	delete(s.Topology.Nodes, bridgeID)
	_, err = Export(s, []string{id})
	expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
}

func fixtureDocument(s candidate.Snapshot, p candidate.Port, shape PortShape, patch VLANFields) Document {
	return Document{Format: FormatV1, Generation: s.Generation, SchemaDigest: s.Schema,
		Ports: []PortDocument{{Binding: p.Binding, Member: s.Topology.Nodes[p.Members[0]].Binding, Shape: shape, VLAN: &patch}}}
}

func marshalDocument(t *testing.T, value Document) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPortVLANExportIsDeterministicBoundedAndRoundTrips(t *testing.T) {
	s := fixtureSnapshot(1)
	accessTag := 9
	a := fixturePort(&s, 20, "internal", observedVLAN("access", &accessTag))
	b := fixturePort(&s, 10, "system", observedVLAN("trunk", nil, 300, 10, 201))
	selection := []string{a.Binding.ManagementID, b.Binding.ManagementID}
	data, err := Export(s, selection)
	if err != nil {
		t.Fatal(err)
	}
	selection[0], selection[1] = selection[1], selection[0]
	p := s.Ports[b.Binding.ManagementID]
	p.VLAN.Trunks = []int{201, 300, 10}
	s.Ports[b.Binding.ManagementID] = p
	s.Revision = candidate.Digest("new transient revision")
	again, err := Export(s, selection)
	if err != nil || !bytes.Equal(data, again) || len(data) > MaxDocumentBytes {
		t.Fatalf("deterministic reordered native set export: %v", err)
	}
	if bytes.Contains(data, []byte("synthetic-")) || bytes.Contains(data, []byte("null")) {
		t.Fatal("raw name, credential or null appeared in the narrow VLAN document")
	}
	doc, err := Decode(data)
	if err != nil || len(doc.Ports) != 2 || doc.Ports[0].Binding != b.Binding ||
		doc.Ports[0].Shape != StandaloneSystem || doc.Ports[0].VLAN.Tag == nil || len(*doc.Ports[0].VLAN.Tag) != 0 ||
		!slices.Equal(*doc.Ports[0].VLAN.Trunks, []int{10, 201, 300}) ||
		doc.Ports[1].Shape != StandaloneInternal || !slices.Equal(*doc.Ports[1].VLAN.Tag, []int{9}) {
		t.Fatalf("decoded deterministic export = %+v, %v", doc, err)
	}
	plan, err := PlanMerge(data, s.Generation, s, nil)
	if err != nil || len(plan.Changes) != 0 || len(plan.Destinations) != 2 ||
		plan.Generation != s.Generation || plan.Revision != s.Revision || plan.SourceSchema != s.Schema {
		t.Fatalf("reimport of current state altered the candidate: %+v, %v", plan, err)
	}
	if intents := draftIntents(t, plan, s); len(intents) != 0 {
		t.Fatalf("no-op generated candidate intents: %+v", intents)
	}
}

func TestMergeDistinguishesOmittedFieldsAndExplicitEmptySets(t *testing.T) {
	s := fixtureSnapshot(1)
	tag := 31
	native := fixturePort(&s, 10, "system", observedVLAN("native-tagged", &tag, 20, 10))
	empty := []int{}
	doc := fixtureDocument(s, native, StandaloneSystem, VLANFields{Trunks: &empty})
	plan, err := PlanMerge(marshalDocument(t, doc), s.Generation, s, nil)
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("explicit empty trunks merge = %+v, %v", plan, err)
	}
	change := plan.Changes[0]
	if change.Destination != native.Binding || *change.After.Mode != "native-tagged" || *change.After.Tag != tag ||
		len(change.After.Trunks) != 0 || !slices.Equal(change.Before.Trunks, []int{10, 20}) ||
		!slices.Equal(s.Ports[native.Binding.ManagementID].VLAN.Trunks, []int{20, 10}) {
		t.Fatalf("omitted fields or live snapshot changed: %+v", change)
	}
	mode := "trunk"
	doc.Ports[0].VLAN = &VLANFields{Mode: &mode}
	if _, err = PlanMerge(marshalDocument(t, doc), s.Generation, s, nil); err == nil {
		t.Fatal("switching to trunk silently cleared an omitted tag")
	}
	doc.Ports[0].VLAN.Tag = &empty
	plan, err = PlanMerge(marshalDocument(t, doc), s.Generation, s, nil)
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].After.Tag != nil ||
		!slices.Equal(plan.Changes[0].After.Trunks, []int{10, 20}) {
		t.Fatalf("explicit tag absence after raw mode change: %+v, %v", plan, err)
	}
	intents := draftIntents(t, plan, s)
	if len(intents) != 1 || !apitypes.ManagementID(intents[0].ID) ||
		intents[0].Operation != "port.vlan.set" || intents[0].Object != native.Binding ||
		intents[0].Value.Tag != nil {
		t.Fatalf("draft intents = %+v", intents)
	}
	intents[0].Value.Trunks[0] = 900
	if draftIntents(t, plan, s)[0].Value.Trunks[0] != 10 {
		t.Fatal("one draft materialization mutated the merge preview")
	}
	if _, err := candidate.Prepare(candidate.Envelope{}, candidate.Command{Operation: "stage", Intents: draftIntents(t, plan, s)}, s); err != nil {
		t.Fatal(err)
	}
	latest := s
	latest.Revision = candidate.Digest("new native configuration")
	_, err = plan.CandidateIntents(latest)
	expectProblem(t, err, "CONFIG_SNAPSHOT_CHANGED")
	latest = s
	latest.Policy = candidate.Digest("new policy")
	_, err = plan.CandidateIntents(latest)
	expectProblem(t, err, "CONFIG_SNAPSHOT_CHANGED")
}

func TestMergeRequiresIdentityMappingAndMatchingShape(t *testing.T) {
	source := fixtureSnapshot(1)
	target := fixtureSnapshot(2)
	tag := 51
	original := fixturePort(&source, 10, "system", observedVLAN("access", &tag))
	differentTag := 12
	destination := fixturePort(&target, 20, "system", observedVLAN("access", &differentTag))
	internal := fixturePort(&target, 30, "internal", observedVLAN("access", &differentTag))
	data, err := Export(source, []string{original.Binding.ManagementID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanMerge(data, target.Generation, target, nil)
	expectProblem(t, err, "CONFIG_MAPPING_REQUIRED")
	input := []Mapping{{SourceID: original.Binding.ManagementID, DestinationID: internal.Binding.ManagementID}}
	_, err = PlanMerge(data, target.Generation, target, input)
	expectProblem(t, err, "CONFIG_PORT_SHAPE_CHANGED")
	input[0].DestinationID = destination.Binding.ManagementID
	plan, err := PlanMerge(data, target.Generation, target, input)
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Before.Tag == nil || *plan.Changes[0].Before.Tag != differentTag ||
		*plan.Changes[0].After.Tag != tag || draftIntents(t, plan, target)[0].Object != destination.Binding ||
		plan.Changes[0].Source != original.Binding {
		t.Fatalf("explicit mapping = %+v, %v", plan, err)
	}
	_, err = PlanMerge(data, source.Generation, target, input)
	expectProblem(t, err, "GENERATION_RECONCILIATION_REQUIRED")
	for description, change := range map[string]func(*candidate.Port){
		"external":    func(p *candidate.Port) { p.Authority = "externally-controlled" },
		"fanout":      func(p *candidate.Port) { p.Members = append(p.Members, fixtureID(500)) },
		"local":       func(p *candidate.Port) { p.LocalPort = true },
		"unqualified": func(p *candidate.Port) { p.Known = false },
	} {
		t.Run(description, func(t *testing.T) {
			p := destination
			change(&p)
			target.Ports[p.Binding.ManagementID] = p
			_, err := PlanMerge(data, target.Generation, target, input)
			expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
		})
	}
	last, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	last.Ports[0].Binding.OVSUUID = fixtureID(70)
	_, err = PlanMerge(marshalDocument(t, last), source.Generation, source, nil)
	expectProblem(t, err, "OBJECT_BINDING_CHANGED")
}

func TestStrictDocumentRejectsNullAndUnknownFields(t *testing.T) {
	s := fixtureSnapshot(1)
	tag := 10
	port := fixturePort(&s, 10, "system", observedVLAN("access", &tag))
	doc := fixtureDocument(s, port, StandaloneSystem, VLANFields{Tag: &[]int{11}})
	base := string(marshalDocument(t, doc))
	for name, bad := range map[string]string{
		"null tag":            strings.Replace(base, `"tag":[11]`, `"tag":null`, 1),
		"null list member":    strings.Replace(base, `"tag":[11]`, `"tag":[null]`, 1),
		"unknown raw setting": strings.Replace(base, `"tag":[11]`, `"tag":[11],"native_command":"synthetic"`, 1),
		"duplicate fields":    strings.Replace(base, `"tag":[11]`, `"tag":[11],"tag":[12]`, 1),
		"case folding":        strings.Replace(base, `"tag":[11]`, `"TAG":[11]`, 1),
		"scalar tag":          strings.Replace(base, `"tag":[11]`, `"tag":11`, 1),
		"multi tag":           strings.Replace(base, `"tag":[11]`, `"tag":[11,12]`, 1),
		"reserved tag":        strings.Replace(base, `"tag":[11]`, `"tag":[4095]`, 1),
		"repeated VLAN":       strings.Replace(base, `"tag":[11]`, `"trunks":[9,9]`, 1),
		"unsupported QinQ":    strings.Replace(base, `"tag":[11]`, `"vlan_mode":"dot1q-tunnel"`, 1),
		"additional JSON":     base + "{}",
		"foreign shape":       strings.Replace(base, `"table":"Port"`, `"table":"Bridge"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode([]byte(bad))
			expectProblem(t, err, "CONFIG_DOCUMENT_INVALID")
		})
	}
	_, err := Decode(make([]byte, MaxDocumentBytes+1))
	expectProblem(t, err, "CONFIG_DOCUMENT_LIMIT")
}

func TestSameGenerationMemberReplacementRequiresExplicitMapping(t *testing.T) {
	s := fixtureSnapshot(1)
	tag := 10
	port := fixturePort(&s, 10, "system", observedVLAN("access", &tag))
	data, err := Export(s, []string{port.Binding.ManagementID})
	if err != nil {
		t.Fatal(err)
	}
	id, memberID := port.Binding.ManagementID, port.Members[0]
	node, member := s.Topology.Nodes[id], s.Topology.Nodes[memberID]
	before := member.Binding
	member.Binding.OVSUUID = fixtureID(5000)
	node.Links = []candidate.Binding{member.Binding}
	s.Topology.Nodes[id], s.Topology.Nodes[memberID] = node, member
	newTag := 11
	port.VLAN.Tag = &newTag
	s.Ports[id] = port
	_, err = PlanMerge(data, s.Generation, s, nil)
	expectProblem(t, err, "CONFIG_MAPPING_REQUIRED")
	plan, err := PlanMerge(data, s.Generation, s, []Mapping{{SourceID: id, DestinationID: id}})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].SourceMember != before ||
		plan.Changes[0].DestinationMember != member.Binding {
		t.Fatalf("reviewed current interface binding = %+v, %v", plan, err)
	}
}

func TestMergeRejectsAmbiguousDestinationAndForeignScope(t *testing.T) {
	s := fixtureSnapshot(1)
	tag := 10
	a := fixturePort(&s, 10, "system", observedVLAN("access", &tag))
	b := fixturePort(&s, 20, "system", observedVLAN("access", &tag))
	data, err := Export(s, []string{a.Binding.ManagementID, b.Binding.ManagementID})
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range [][]Mapping{
		{{SourceID: a.Binding.ManagementID, DestinationID: b.Binding.ManagementID}},
		{
			{SourceID: a.Binding.ManagementID, DestinationID: a.Binding.ManagementID},
			{SourceID: a.Binding.ManagementID, DestinationID: b.Binding.ManagementID},
		},
		{{SourceID: fixtureID(40), DestinationID: b.Binding.ManagementID}},
	} {
		_, err := PlanMerge(data, s.Generation, s, mapping)
		expectProblem(t, err, "CONFIG_MAPPING_INVALID")
	}
	_, err = Export(s, []string{a.Binding.ManagementID, a.Binding.ManagementID})
	expectProblem(t, err, "CONFIG_SCOPE_INVALID")
}

func TestExactRestoreStagesCompleteNativeVLANFieldsThroughCandidate(t *testing.T) {
	source := fixtureSnapshot(1)
	current := fixtureSnapshot(1)
	port := fixturePort(&source, 10, "system", observedVLAN("trunk", nil, 23, 15))
	beforeTag := 91
	currentPort := fixturePort(&current, 10, "system", observedVLAN("access", &beforeTag))
	id := port.Binding.ManagementID
	data, err := Export(source, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanExactRestore(data, current.Generation, current, nil)
	expectProblem(t, err, "CONFIG_MAPPING_REQUIRED")
	mappings := []Mapping{{SourceID: id, DestinationID: id}}
	_, err = PlanExactRestore(data, current.Generation, current, mappings)
	expectProblem(t, err, "CONFIG_MAPPING_REQUIRED")
	mappings[0].DestinationMemberID = fixtureID(200)
	_, err = PlanExactRestore(data, current.Generation, current, mappings)
	expectProblem(t, err, "CONFIG_DESTINATION_MEMBER_CHANGED")
	mappings[0].DestinationMemberID = currentPort.Members[0]
	restore, err := PlanExactRestore(data, current.Generation, current, mappings)
	if err != nil || len(restore.Changes) != 1 {
		t.Fatalf("exact restore did not bind a changed Port: %+v, %v", restore, err)
	}
	intents := draftIntents(t, restore, current)
	change := restore.Changes[0]
	if len(intents) != 1 || change.DestinationMember.ManagementID != mappings[0].DestinationMemberID ||
		change.Before.Tag == nil || *change.Before.Tag != beforeTag ||
		intents[0].Operation != "port.vlan.set" || intents[0].Object != currentPort.Binding ||
		candidate.Digest(intents[0].Value) != candidate.Digest(observedVLAN("trunk", nil, 15, 23)) {
		t.Fatalf("restore intent did not exactly replace the scoped native fields: %+v", restore)
	}
	draft, err := candidate.Prepare(candidate.Envelope{}, candidate.Command{Operation: "stage", Intents: intents}, current)
	if err != nil || len(draft.Candidate.Intents) != 1 || *current.Ports[id].VLAN.Tag != beforeTag {
		t.Fatalf("restore did not use ordinary draft staging: %+v, %v", draft, err)
	}
	member := current.Topology.Nodes[currentPort.Members[0]]
	member.Binding.OVSUUID = fixtureID(501)
	current.Topology.Nodes[currentPort.Members[0]] = member
	current.Topology.Nodes[id] = candidate.TopologyNode{Binding: currentPort.Binding, Links: []candidate.Binding{member.Binding}}
	_, err = restore.CandidateIntents(current)
	expectProblem(t, err, "CONFIG_SNAPSHOT_CHANGED")
}

func TestExactRestoreRefusesOmissionsAndUnrepresentableNativeBeforeImages(t *testing.T) {
	current := fixtureSnapshot(2)
	tag := 10
	port := fixturePort(&current, 10, "internal", observedVLAN("access", &tag))
	id := port.Binding.ManagementID
	mappings := []Mapping{{SourceID: id, DestinationID: id, DestinationMemberID: port.Members[0]}}
	mode, empty, updated := "access", []int{}, []int{20}
	for _, omission := range []VLANFields{{Tag: &updated, Trunks: &empty}, {Mode: &mode, Trunks: &empty}, {Mode: &mode, Tag: &updated}} {
		partial := fixtureDocument(current, port, StandaloneInternal, omission)
		_, err := PlanExactRestore(marshalDocument(t, partial), current.Generation, current, mappings)
		expectProblem(t, err, "CONFIG_EXACT_FIELDS_REQUIRED")
	}
	complete := fixtureDocument(current, port, StandaloneInternal, VLANFields{Mode: &mode, Tag: &updated, Trunks: &empty})
	data := marshalDocument(t, complete)
	port.VLAN.Mode = nil
	current.Ports[id] = port
	_, err := PlanExactRestore(data, current.Generation, current, mappings)
	expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
	port.VLAN.Mode, port.VLAN.Trunks = &mode, []int{30}
	current.Ports[id] = port
	_, err = PlanExactRestore(data, current.Generation, current, mappings)
	expectProblem(t, err, "CONFIG_EXACT_BEFORE_UNSUPPORTED")
	port.VLAN.Trunks, port.VLAN.CVLANs = nil, []int{30}
	current.Ports[id] = port
	_, err = PlanExactRestore(data, current.Generation, current, mappings)
	expectProblem(t, err, "CONFIG_PORT_UNPROVEN")
}

func expectProblem(t *testing.T, err error, code string) {
	t.Helper()
	p, ok := err.(*apitypes.Problem)
	if !ok || p.Code != code {
		t.Fatalf("problem = %v, want %s", err, code)
	}
}

func draftIntents(t *testing.T, plan MergePlan, current candidate.Snapshot) []candidate.Intent {
	t.Helper()
	intents, err := plan.CandidateIntents(current)
	if err != nil {
		t.Fatal(err)
	}
	return intents
}
