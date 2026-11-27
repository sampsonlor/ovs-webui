package runtimecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSystemdRestartsOnlySealedProductUnits(t *testing.T) {
	for _, target := range []Target{TargetManager, TargetWeb} {
		t.Run(string(target), func(t *testing.T) {
			fixture := newSystemdFixture(t, target)
			ctx := context.Background()
			witness, err := fixture.supervisor.Inspect(ctx, target)
			if err != nil || witness != (UnitState{
				InvocationID: "0102030405060708090a0b0c0d0e0f10", MainPID: 271, Active: true,
			}) {
				t.Fatalf("inspect = %#v, %v", witness, err)
			}
			job, err := fixture.supervisor.TryRestartAfter(ctx, target, witness)
			if err != nil || job != jobPrefix+"37" || fixture.dispatches != 1 {
				t.Fatalf("job = %q, %v, dispatches = %d", job, err, fixture.dispatches)
			}
		})
	}
}

func TestSystemdRefusesUnprovenTargetBeforeDispatch(t *testing.T) {
	tests := []struct {
		name   string
		change func(*systemdFixture)
		code   string
	}{
		{"non-system owner", func(f *systemdFixture) { f.ownerPID = 88 }, "SYSTEMD_UNAVAILABLE"},
		{"non-root owner", func(f *systemdFixture) { f.ownerUID = 22 }, "SYSTEMD_UNAVAILABLE"},
		{"untrusted unit fragment", func(f *systemdFixture) { f.filesTrusted = false }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"alias target", func(f *systemdFixture) { f.unitProps["Id"] = variant("s", "openvswitch-switch.service") }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"drop-in", func(f *systemdFixture) { f.unitProps["DropInPaths"] = variant("as", []string{"/etc/override.conf"}) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"cached unit needs reload", func(f *systemdFixture) { f.unitProps["NeedDaemonReload"] = variant("b", true) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"inverse part-of", func(f *systemdFixture) { f.unitProps["ConsistsOf"] = variant("as", []string{"ovsdb-server.service"}) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"inverse bind", func(f *systemdFixture) { f.unitProps["BoundBy"] = variant("as", []string{"ovs-vswitchd.service"}) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"implicit stop propagation", func(f *systemdFixture) { f.unitProps["PropagatesStopTo"] = variant("as", []string{"switch.service"}) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"unsupported dependency evidence", func(f *systemdFixture) { delete(f.unitProps, "StopPropagatedFrom") }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"unit may change host state", func(f *systemdFixture) { f.unitProps["FailureAction"] = variant("s", "reboot") }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"changed service executable", func(f *systemdFixture) { f.setExecStart("/usr/bin/true") }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"start hook", func(f *systemdFixture) { f.serviceProps["ExecStartPost"] = f.serviceProps["ExecStart"] }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"mismatched job", func(f *systemdFixture) { f.unitProps["Job"] = variant("(uo)", []any{15, jobPrefix + "16"}) }, "SYSTEMD_UNIT_UNQUALIFIED"},
		{"systemd already has a job", func(f *systemdFixture) { f.unitProps["Job"] = variant("(uo)", []any{15, jobPrefix + "15"}) }, "SYSTEMD_UNIT_BUSY"},
		{"inactive unit", func(f *systemdFixture) { f.unitProps["ActiveState"] = variant("s", "inactive") }, "SYSTEMD_UNIT_INACTIVE"},
		{"changed midway", func(f *systemdFixture) {
			f.changeOnSecondRead = func() { f.unitProps["NeedDaemonReload"] = variant("b", true) }
		}, "SYSTEMD_UNIT_CHANGED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSystemdFixture(t, TargetWeb)
			tc.change(fixture)
			_, err := fixture.supervisor.TryRestart(context.Background(), TargetWeb)
			assertSystemdError(t, err, tc.code, false)
			if fixture.dispatches != 0 {
				t.Fatal("unsafe request reached restart method")
			}
		})
	}

	t.Run("unregistered input", func(t *testing.T) {
		fixture := newSystemdFixture(t, TargetWeb)
		_, err := fixture.supervisor.TryRestart(context.Background(), Target("ovs-vswitchd.service"))
		assertSystemdError(t, err, "SYSTEMD_TARGET_UNSUPPORTED", false)
		if fixture.calls != 0 {
			t.Fatal("unregistered target reached system bus")
		}
	})
}

func TestSystemdExposesPendingJobAndRejectsStaleWitness(t *testing.T) {
	fixture := newSystemdFixture(t, TargetWeb)
	ctx := context.Background()
	fixture.unitProps["Job"] = variant("(uo)", []any{17, jobPrefix + "17"})
	state, err := fixture.supervisor.Inspect(ctx, TargetWeb)
	if err != nil || !state.JobPending || !state.Active || state.MainPID != 271 || state.InvocationID == "" {
		t.Fatalf("inspect pending = %#v, %v", state, err)
	}
	fixture.unitProps["Job"] = variant("(uo)", []any{0, "/"})
	fixture.serviceProps["MainPID"] = variant("u", 301)
	fixture.serviceProps["ExecMainPID"] = variant("u", 301)
	_, err = fixture.supervisor.TryRestartAfter(ctx, TargetWeb, UnitState{
		InvocationID: state.InvocationID, MainPID: state.MainPID, Active: true,
	})
	assertSystemdError(t, err, "SYSTEMD_UNIT_CHANGED", false)
	if fixture.dispatches != 0 {
		t.Fatal("stale witness reached restart method")
	}
}

func TestSystemdDispatchDoesNotRetryOrClaimCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		err        error
	}{
		{"transport failure", "", errors.New("uncertain write")},
		{"wrong object path", managerPath + "/unit/example", nil},
		{"bad job ID", jobPrefix + "037", nil},
		{"empty job", jobPrefix + "0", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSystemdFixture(t, TargetManager)
			fixture.restartPath = tc.path
			fixture.restartErr = tc.err
			path, err := fixture.supervisor.TryRestart(context.Background(), TargetManager)
			assertSystemdError(t, err, "SYSTEMD_RESTART_OUTCOME_UNKNOWN", true)
			if path != "" || fixture.dispatches != 1 {
				t.Fatalf("job = %q, dispatches = %d", path, fixture.dispatches)
			}
		})
	}
}

func TestSystemdCommandOutputLimitAppliesToPipeCopies(t *testing.T) {
	var output boundedSystemdOutput
	source := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", replyLimit+1))}
	count, err := io.Copy(&output, source)
	if err == nil || count != replyLimit || output.buffer.Len() != replyLimit {
		t.Fatalf("copy = %d, %v, buffered = %d", count, err, output.buffer.Len())
	}
}

func assertSystemdError(t *testing.T, err error, code string, unknown bool) {
	t.Helper()
	var typed *SystemdError
	if !errors.As(err, &typed) || typed.Code != code || typed.OutcomeUnknown != unknown {
		t.Fatalf("error = %#v, want %s (unknown = %t)", err, code, unknown)
	}
}

type systemdFixture struct {
	t                            *testing.T
	unit                         productUnit
	supervisor                   *systemdSupervisor
	unitProps, serviceProps      busProperties
	ownerPID, ownerUID           uint32
	filesTrusted                 bool
	calls, unitReads, dispatches int
	restartPath                  string
	restartErr                   error
	changeOnSecondRead           func()
}

func newSystemdFixture(t *testing.T, target Target) *systemdFixture {
	t.Helper()
	unit, err := resolveUnit(target)
	if err != nil {
		t.Fatal(err)
	}
	f := &systemdFixture{
		t: t, unit: unit, ownerPID: 1, filesTrusted: true, restartPath: jobPrefix + "37",
		unitProps:    make(busProperties),
		serviceProps: make(busProperties),
	}
	for _, rule := range unitGraph {
		f.unitProps[rule.key] = variant("as", []string{})
	}
	f.unitProps["Names"] = variant("as", []string{unit.name})
	f.unitProps["Requires"] = variant("as", []string{"sysinit.target", "system.slice", "systemd-journald.socket", "-.mount"})
	f.unitProps["Wants"] = variant("as", []string{"tmp.mount"})
	f.unitProps["Conflicts"] = variant("as", []string{"shutdown.target"})
	for key, value := range map[string]string{
		"Id": unit.name, "LoadState": "loaded", "ActiveState": "active", "SubState": "running",
		"FragmentPath": "/usr/lib/systemd/system/" + unit.name, "JobTimeoutAction": "none",
		"StartLimitAction": "none", "FailureAction": "none", "SuccessAction": "none",
	} {
		f.unitProps[key] = variant("s", value)
	}
	for _, key := range []string{"NeedDaemonReload", "StopWhenUnneeded", "RefuseManualStart", "RefuseManualStop"} {
		f.unitProps[key] = variant("b", false)
	}
	f.unitProps["DropInPaths"] = variant("as", []string{})
	f.unitProps["Job"] = variant("(uo)", []any{0, "/"})
	f.unitProps["InvocationID"] = variant("ay", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	for key, value := range map[string]string{
		"Type": unit.serviceType, "User": unit.user, "Group": "ovs-webui-web", "KillMode": "control-group",
		"RootDirectory": "", "RootImage": "",
	} {
		f.serviceProps[key] = variant("s", value)
	}
	for _, key := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost"} {
		f.serviceProps[key] = variant(executionSignature, []any{})
	}
	f.serviceProps["MainPID"] = variant("u", 271)
	f.serviceProps["ExecMainPID"] = variant("u", 271)
	f.setExecStart(unit.executable)
	f.supervisor = &systemdSupervisor{
		call: f.call,
		verifyFiles: func(actual productUnit, fragment string) bool {
			return f.filesTrusted && actual == f.unit && fragment == "/usr/lib/systemd/system/"+f.unit.name
		},
	}
	return f
}

func (f *systemdFixture) setExecStart(executable string) {
	f.serviceProps["ExecStart"] = variant(executionSignature, [][]any{
		{executable, []string{executable}, false, 0, 0, 0, 0, 271, 0, 0},
	})
}

func variant(signature string, value any) busVariant {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return busVariant{Type: signature, Data: data}
}

func (f *systemdFixture) call(_ context.Context, args ...string) ([]byte, error) {
	const owner = ":1.27"
	f.calls++
	var signature string
	var value any
	switch args[3] {
	case "GetNameOwner":
		f.expectCall(args, systemBus, systemBusPath, systemBus, "GetNameOwner", "s", managerBus)
		signature, value = "s", owner
	case "GetConnectionUnixProcessID":
		f.expectCall(args, systemBus, systemBusPath, systemBus, "GetConnectionUnixProcessID", "s", owner)
		signature, value = "u", f.ownerPID
	case "GetConnectionUnixUser":
		f.expectCall(args, systemBus, systemBusPath, systemBus, "GetConnectionUnixUser", "s", owner)
		signature, value = "u", f.ownerUID
	case "GetUnit":
		f.expectCall(args, owner, managerPath, managerIface, "GetUnit", "s", f.unit.name)
		signature, value = "o", f.unit.path
	case "GetAll":
		if args[5] == unitIface {
			f.expectCall(args, owner, f.unit.path, propertyIface, "GetAll", "s", unitIface)
			f.unitReads++
			if f.unitReads == 2 && f.changeOnSecondRead != nil {
				f.changeOnSecondRead()
			}
			signature, value = "a{sv}", f.unitProps
		} else {
			f.expectCall(args, owner, f.unit.path, propertyIface, "GetAll", "s", serviceIface)
			signature, value = "a{sv}", f.serviceProps
		}
	case "TryRestartUnit":
		f.expectCall(args, owner, managerPath, managerIface, "TryRestartUnit", "ss", f.unit.name, "fail")
		f.dispatches++
		if f.restartErr != nil {
			return nil, f.restartErr
		}
		signature, value = "o", f.restartPath
	default:
		f.t.Fatalf("unexpected system bus call %v", args)
	}
	return json.Marshal(map[string]any{"type": signature, "data": []any{value}})
}

func (f *systemdFixture) expectCall(actual []string, expected ...string) {
	f.t.Helper()
	if !reflect.DeepEqual(actual, expected) {
		f.t.Fatalf("bus call = %v, want %v", actual, expected)
	}
}
