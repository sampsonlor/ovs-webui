package candidate

import (
	"regexp"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

const BridgeCreate = "bridge.create-isolated"
const BridgeCreationMarker = "ovs-webui.bridge-creation"

// BridgeCreation is a manager-minted graph, never a caller-selected identity.
// BeforePresent/AfterPresent also describe the private compensation plan.
type BridgeCreation struct {
	Name          string  `json:"name"`
	Root          string  `json:"root_uuid"`
	Port          Binding `json:"local_port"`
	Interface     Binding `json:"local_interface"`
	BeforePresent bool    `json:"before_present"`
	AfterPresent  bool    `json:"after_present"`
}

type CreationSnapshot struct {
	Root, Authority string
	Supported       bool
	Capacity        bool
	AllowedNames    map[string]bool
	Names           map[string]bool
	Objects         map[string]Binding
	Retired         map[string]bool
}

var bridgeName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$`)

func ValidBridgeName(name string) bool { return bridgeName.MatchString(name) }

func CreationBindings(i StoredIntent) []Binding {
	if i.Creation == nil {
		return nil
	}
	return []Binding{i.Object, i.Creation.Port, i.Creation.Interface}
}

func newBridgeIntent(in Intent, s Snapshot) (StoredIntent, error) {
	if !ValidBridgeName(in.Name) || in.Object != (Binding{}) || in.Mode != "" || in.LACP != "" || in.Fallback != "" || len(in.Members) != 0 || Digest(in.Value) != Digest(VLAN{}) {
		return StoredIntent{}, apitypes.Fail(422, "INVALID_ISOLATED_BRIDGE")
	}
	bind := func(table string) Binding {
		return Binding{ManagementID: repository.NewID(), OVSUUID: repository.NewID(), Table: table, Generation: s.Generation}
	}
	return StoredIntent{ID: in.ID, Operation: BridgeCreate, Object: bind("Bridge"), Schema: s.Schema,
		Dependency: Digest([]any{s.Creation.Root, in.Name}), Value: normalize(VLAN{}), Before: normalize(VLAN{}),
		Creation: &BridgeCreation{Name: in.Name, Root: s.Creation.Root, Port: bind("Port"), Interface: bind("Interface"), AfterPresent: true}}, nil
}

func creationProblem(i StoredIntent, s Snapshot) string {
	c := i.Creation
	if c == nil || i.Operation != BridgeCreate {
		return "INVALID_ISOLATED_BRIDGE"
	}
	if i.Object.Generation != s.Generation || c.Root != s.Creation.Root {
		return "GENERATION_RECONCILIATION_REQUIRED"
	}
	if i.Schema != s.Schema {
		return "SCHEMA_CHANGED"
	}
	if !c.BeforePresent && c.AfterPresent && s.Creation.Names[c.Name] {
		return "OBJECT_NAME_IN_USE"
	}
	for _, b := range CreationBindings(i) {
		current, exists := s.Creation.Objects[b.OVSUUID]
		if c.BeforePresent {
			if !exists || current != b {
				return "OBJECT_BINDING_CHANGED"
			}
		} else if exists {
			return "OBJECT_BINDING_CHANGED"
		} else if c.AfterPresent && s.Creation.Retired[b.OVSUUID] {
			return "CREATION_IDENTITY_CONSUMED"
		}
	}
	return ""
}

func creationChecks(i StoredIntent, s Snapshot) []Gate {
	out := []Gate{}
	if i.Creation != nil && !i.Creation.BeforePresent && i.Creation.AfterPresent && !s.Creation.Capacity {
		out = append(out, gate("BRIDGE_INVENTORY_CAPACITY", "blocked", i.ID))
	}
	if !s.Creation.Supported {
		out = append(out, gate("BRIDGE_LIFECYCLE_SCHEMA_UNSUPPORTED", "blocked", i.ID))
	}
	if s.Creation.Authority != "local-managed" || i.Creation == nil || !s.Creation.AllowedNames[i.Creation.Name] {
		out = append(out, gate("BRIDGE_CREATION_AUTHORITY_REQUIRED", "blocked", i.ID))
	}
	return append(out, gate("ISOLATED_BRIDGE_NO_UPLINK_OR_IP_CONFIGURATION", "allowed", i.ID))
}

func cloneCreation(c *BridgeCreation) *BridgeCreation {
	if c == nil {
		return nil
	}
	copy := *c
	return &copy
}

func hasCreation(intents []StoredIntent) bool {
	for _, i := range intents {
		if i.Operation == BridgeCreate {
			return true
		}
	}
	return false
}

func stageCreation(c Candidate, cmd Command, s Snapshot) (Candidate, error) {
	if len(cmd.Intents) != 1 || len(c.Intents) > 1 || cmd.Intents[0].Operation != BridgeCreate || !apitypes.ManagementID(cmd.Intents[0].ID) {
		return c, apitypes.Fail(422, "ISOLATED_BRIDGE_SINGLE_INTENT_REQUIRED")
	}
	i, err := newBridgeIntent(cmd.Intents[0], s)
	if err != nil {
		return c, err
	}
	if c.Generation != nil && *c.Generation != s.Generation {
		return c, apitypes.Fail(409, "GENERATION_RECONCILIATION_REQUIRED")
	}
	if len(c.Intents) == 1 {
		prior := c.Intents[0]
		if prior.Operation != BridgeCreate || prior.ID != i.ID || prior.Creation == nil || prior.Creation.Name != i.Creation.Name {
			return c, apitypes.Fail(409, "CREATION_RESTAGE_REQUIRED")
		}
		i = prior
	}
	if problem := creationProblem(i, s); problem != "" {
		return c, apitypes.Fail(409, problem)
	}
	c.Intents = []StoredIntent{i}
	c.Generation = &s.Generation
	c.BaseRevision = &s.Revision
	return c, nil
}
