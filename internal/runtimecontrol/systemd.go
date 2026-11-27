package runtimecontrol

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Target string

const (
	TargetManager Target = "manager"
	TargetWeb     Target = "web"
)

type UnitState struct {
	InvocationID string
	MainPID      int
	Active       bool
	JobPending   bool
}

type Supervisor interface {
	Inspect(context.Context, Target) (UnitState, error)
	TryRestart(context.Context, Target) (string, error)
}

// FencedSupervisor compares the service incarnation to the caller's durable witness before dispatch.
type FencedSupervisor interface {
	Supervisor
	TryRestartAfter(context.Context, Target, UnitState) (string, error)
}

type SystemdError struct {
	Code           string
	OutcomeUnknown bool
}

func (e *SystemdError) Error() string { return e.Code }

const (
	systemBus     = "org.freedesktop.DBus"
	systemBusPath = "/org/freedesktop/DBus"
	managerBus    = "org.freedesktop.systemd1"
	managerPath   = "/org/freedesktop/systemd1"
	managerIface  = managerBus + ".Manager"
	unitIface     = managerBus + ".Unit"
	serviceIface  = managerBus + ".Service"
	propertyIface = systemBus + ".Properties"

	jobPrefix       = managerPath + "/job/"
	supervisorLimit = 2 * time.Second
	replyLimit      = 64 * 1024
)

var uniqueSystemdName = regexp.MustCompile(`^:[0-9]{1,10}\.[0-9]{1,10}$`)

type productUnit struct {
	name, path, executable, serviceType, user string
}

func resolveUnit(target Target) (productUnit, error) {
	switch target {
	case TargetManager:
		return productUnit{
			name: "ovs-mgrd.service", path: managerPath + "/unit/ovs_2dmgrd_2eservice",
			executable: "/usr/libexec/ovs-mgrd", serviceType: "notify", user: "root",
		}, nil
	case TargetWeb:
		return productUnit{
			name: "ovs-webd.service", path: managerPath + "/unit/ovs_2dwebd_2eservice",
			executable: "/usr/libexec/ovs-webd", serviceType: "simple", user: "ovs-webui-web",
		}, nil
	default:
		return productUnit{}, &SystemdError{Code: "SYSTEMD_TARGET_UNSUPPORTED"}
	}
}

type systemdSupervisor struct {
	call        func(context.Context, ...string) ([]byte, error)
	verifyFiles func(productUnit, string) bool
}

func (s *systemdSupervisor) Inspect(ctx context.Context, target Target) (UnitState, error) {
	ctx, cancel := context.WithTimeout(ctx, supervisorLimit)
	defer cancel()
	unit, err := resolveUnit(target)
	if err != nil {
		return UnitState{}, err
	}
	owner, err := s.managerOwner(ctx)
	if err != nil {
		return UnitState{}, err
	}
	return s.inspect(ctx, owner, unit)
}

func (s *systemdSupervisor) TryRestart(ctx context.Context, target Target) (string, error) {
	return s.tryRestart(ctx, target, nil)
}

func (s *systemdSupervisor) TryRestartAfter(ctx context.Context, target Target, witness UnitState) (string, error) {
	return s.tryRestart(ctx, target, &witness)
}

func (s *systemdSupervisor) tryRestart(ctx context.Context, target Target, witness *UnitState) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, supervisorLimit)
	defer cancel()
	unit, err := resolveUnit(target)
	if err != nil {
		return "", err
	}
	owner, err := s.managerOwner(ctx)
	if err != nil {
		return "", err
	}
	observed, err := s.inspect(ctx, owner, unit)
	if err != nil {
		return "", err
	}
	if observed.JobPending {
		return "", &SystemdError{Code: "SYSTEMD_UNIT_BUSY"}
	}
	if !observed.Active {
		return "", &SystemdError{Code: "SYSTEMD_UNIT_INACTIVE"}
	}
	if witness != nil && *witness != observed {
		return "", &SystemdError{Code: "SYSTEMD_UNIT_CHANGED"}
	}
	if ctx.Err() != nil {
		return "", &SystemdError{Code: "SYSTEMD_UNAVAILABLE"}
	}

	// Each dispatch is attempted once. A transport error after this point cannot prove PID 1 declined it.
	var job string
	if !s.get(ctx, &job, "o", owner, managerPath, managerIface, "TryRestartUnit", "ss", unit.name, "fail") || !validJobPath(job) {
		return "", &SystemdError{Code: "SYSTEMD_RESTART_OUTCOME_UNKNOWN", OutcomeUnknown: true}
	}
	return job, nil
}

func (s *systemdSupervisor) managerOwner(ctx context.Context) (string, error) {
	var owner string
	var pid, uid uint32
	if !s.get(ctx, &owner, "s", systemBus, systemBusPath, systemBus, "GetNameOwner", "s", managerBus) ||
		!uniqueSystemdName.MatchString(owner) ||
		!s.get(ctx, &pid, "u", systemBus, systemBusPath, systemBus, "GetConnectionUnixProcessID", "s", owner) || pid != 1 ||
		!s.get(ctx, &uid, "u", systemBus, systemBusPath, systemBus, "GetConnectionUnixUser", "s", owner) || uid != 0 {
		return "", &SystemdError{Code: "SYSTEMD_UNAVAILABLE"}
	}
	return owner, nil
}

func (s *systemdSupervisor) inspect(ctx context.Context, owner string, unit productUnit) (UnitState, error) {
	var path string
	var properties, service busProperties
	if !s.get(ctx, &path, "o", owner, managerPath, managerIface, "GetUnit", "s", unit.name) ||
		path != unit.path ||
		!s.get(ctx, &properties, "a{sv}", owner, unit.path, propertyIface, "GetAll", "s", unitIface) ||
		!s.get(ctx, &service, "a{sv}", owner, unit.path, propertyIface, "GetAll", "s", serviceIface) {
		return UnitState{}, &SystemdError{Code: "SYSTEMD_UNIT_UNAVAILABLE"}
	}
	state, ok := qualifiedUnit(properties, service, unit)
	if !ok || !s.verifyFiles(unit, properties.str("FragmentPath")) {
		return UnitState{}, &SystemdError{Code: "SYSTEMD_UNIT_UNQUALIFIED"}
	}

	// Unit and Service GetAll replies are separate observations. A changed Unit
	// witness means the identity, effective graph or pending jobs were not stable.
	var latest busProperties
	if !s.get(ctx, &latest, "a{sv}", owner, unit.path, propertyIface, "GetAll", "s", unitIface) ||
		!sameUnitProof(properties, latest) {
		return UnitState{}, &SystemdError{Code: "SYSTEMD_UNIT_CHANGED"}
	}
	return state, nil
}

func (s *systemdSupervisor) get(ctx context.Context, value any, signature string, args ...string) bool {
	reply, err := s.call(ctx, args...)
	if err != nil || len(reply) > replyLimit {
		return false
	}
	var message struct {
		Type string
		Data []json.RawMessage
	}
	return json.Unmarshal(reply, &message) == nil && message.Type == signature &&
		len(message.Data) == 1 && jsonValue(message.Data[0], value)
}

type boundedSystemdOutput struct{ buffer bytes.Buffer }

func (b *boundedSystemdOutput) Write(data []byte) (int, error) {
	if len(data) > replyLimit-b.buffer.Len() {
		return 0, errors.New("systemd D-Bus reply too large")
	}
	return b.buffer.Write(data)
}

type busVariant struct {
	Type string
	Data json.RawMessage
}

type busProperties map[string]busVariant

func jsonValue(data json.RawMessage, value any) bool {
	return !bytes.Equal(bytes.TrimSpace(data), []byte("null")) && json.Unmarshal(data, value) == nil
}

func (p busProperties) read(key, signature string, value any) bool {
	v, ok := p[key]
	return ok && v.Type == signature && jsonValue(v.Data, value)
}

func (p busProperties) str(key string) string {
	var value string
	p.read(key, "s", &value)
	return value
}

func (p busProperties) boolIs(key string, expected bool) bool {
	var value bool
	return p.read(key, "b", &value) && value == expected
}

type graphRule struct {
	key     string
	allowed []string
}

var unitGraph = []graphRule{
	{"Names", nil}, // Verified against the target's exact canonical name.
	{"Requires", []string{"sysinit.target", "basic.target", "system.slice", "systemd-journald.socket", "-.mount"}},
	{"Wants", []string{"tmp.mount"}},
	{"Conflicts", []string{"shutdown.target"}},
	{"RequiredBy", nil},
	{"Requisite", nil},
	{"RequisiteOf", nil},
	{"BindsTo", nil},
	{"BoundBy", nil},
	{"PartOf", nil},
	{"ConsistsOf", nil},
	{"ConflictedBy", nil},
	{"PropagatesStopTo", nil},
	{"StopPropagatedFrom", nil},
	{"OnFailure", nil},
	{"OnSuccess", nil},
	{"Upholds", nil},
}

var unitProofFields = []string{
	"Id", "LoadState", "ActiveState", "SubState", "FragmentPath", "DropInPaths",
	"NeedDaemonReload", "StopWhenUnneeded", "RefuseManualStart", "RefuseManualStop",
	"InvocationID", "Job", "JobTimeoutAction", "StartLimitAction", "FailureAction", "SuccessAction",
}

func sameUnitProof(first, latest busProperties) bool {
	equal := func(key string) bool {
		return first[key].Type == latest[key].Type && bytes.Equal(first[key].Data, latest[key].Data)
	}
	for _, key := range unitProofFields {
		if !equal(key) {
			return false
		}
	}
	for _, rule := range unitGraph {
		if !equal(rule.key) {
			return false
		}
	}
	return true
}

func qualifiedUnit(p, service busProperties, unit productUnit) (UnitState, bool) {
	var active, substate, fragment string
	var dropIns []string
	if p.str("Id") != unit.name || p.str("LoadState") != "loaded" ||
		!p.read("ActiveState", "s", &active) || !p.read("SubState", "s", &substate) ||
		!p.read("FragmentPath", "s", &fragment) || !allowedFragment(fragment, unit.name) ||
		!p.read("DropInPaths", "as", &dropIns) || len(dropIns) != 0 ||
		!p.boolIs("NeedDaemonReload", false) || !p.boolIs("StopWhenUnneeded", false) ||
		!p.boolIs("RefuseManualStart", false) || !p.boolIs("RefuseManualStop", false) ||
		!safeUnitGraph(p, unit.name) ||
		!sealedService(service, unit) {
		return UnitState{}, false
	}
	for _, key := range []string{"JobTimeoutAction", "StartLimitAction", "FailureAction", "SuccessAction"} {
		if p.str(key) != "none" {
			return UnitState{}, false
		}
	}

	var pid, execPID uint32
	var id []uint8
	var job []json.RawMessage
	if !service.read("MainPID", "u", &pid) || !service.read("ExecMainPID", "u", &execPID) ||
		!p.read("InvocationID", "ay", &id) || len(id) != 16 || !p.read("Job", "(uo)", &job) {
		return UnitState{}, false
	}
	pending, valid := pendingJob(job)
	running := active == "active" && substate == "running"
	state := UnitState{JobPending: pending, Active: running, MainPID: int(pid)}
	if !bytes.Equal(id, make([]byte, 16)) {
		state.InvocationID = hex.EncodeToString(id)
	}
	if !valid || (running && (pid == 0 || pid != execPID || state.InvocationID == "")) {
		return UnitState{}, false
	}
	return state, true
}

func safeUnitGraph(p busProperties, name string) bool {
	for _, rule := range unitGraph {
		var destinations []string
		if !p.read(rule.key, "as", &destinations) {
			return false
		}
		if rule.key == "Names" {
			if !slices.Equal(destinations, []string{name}) {
				return false
			}
			continue
		}
		for _, destination := range destinations {
			if !slices.Contains(rule.allowed, destination) {
				return false
			}
		}
	}
	return true
}

func allowedFragment(path, name string) bool {
	return path == "/usr/lib/systemd/system/"+name || path == "/lib/systemd/system/"+name ||
		path == "/etc/systemd/system/"+name
}

const executionSignature = "a(sasbttttuii)"

func sealedService(p busProperties, unit productUnit) bool {
	if p.str("Type") != unit.serviceType || p.str("User") != unit.user ||
		p.str("Group") != "ovs-webui-web" || p.str("KillMode") != "control-group" {
		return false
	}
	for _, key := range []string{"RootDirectory", "RootImage"} {
		if value, exists := p[key]; !exists || value.Type != "s" || !bytes.Equal(value.Data, []byte(`""`)) {
			return false
		}
	}
	for _, key := range []string{"ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecCondition"} {
		var commands []json.RawMessage
		if !p.read(key, executionSignature, &commands) || len(commands) != 0 {
			return false
		}
	}
	var commands [][]json.RawMessage
	if !p.read("ExecStart", executionSignature, &commands) || len(commands) != 1 || len(commands[0]) != 10 {
		return false
	}
	var executable string
	var argv []string
	var ignoreFailure bool
	return jsonValue(commands[0][0], &executable) && executable == unit.executable &&
		jsonValue(commands[0][1], &argv) && len(argv) > 0 && argv[0] == unit.executable &&
		jsonValue(commands[0][2], &ignoreFailure) && !ignoreFailure
}

func pendingJob(fields []json.RawMessage) (bool, bool) {
	var id uint32
	var path string
	if len(fields) != 2 || !jsonValue(fields[0], &id) || !jsonValue(fields[1], &path) {
		return false, false
	}
	if id == 0 {
		return false, path == "/"
	}
	return true, path == jobPrefix+strconv.FormatUint(uint64(id), 10)
}

func validJobPath(path string) bool {
	value, ok := strings.CutPrefix(path, jobPrefix)
	if !ok {
		return false
	}
	id, err := strconv.ParseUint(value, 10, 32)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == value
}
