package runtimecontrol

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	busctlPath = "/usr/bin/busctl"
	busSocket  = "/run/dbus/system_bus_socket"
)

// NewSystemd uses the fixed host system bus. The caller supplies authorization and durability.
func NewSystemd() FencedSupervisor {
	return &systemdSupervisor{call: callSystemBus, verifyFiles: verifyProductFiles}
}

func callSystemBus(ctx context.Context, call ...string) ([]byte, error) {
	if !trustedBusSocket() {
		return nil, errors.New("systemd system bus unavailable")
	}
	binary, err := openSystemFile(busctlPath, true)
	if err != nil {
		return nil, err
	}
	defer binary.Close()

	// Pin the trusted ELF through exec. Full path, fixed bus, no activation,
	// interactive prompts, or caller-environment overrides.
	args := []string{
		"--address=unix:path=" + busSocket, "--json=short", "--no-pager",
		"--allow-interactive-authorization=no", "--auto-start=no", "--timeout=1s", "call",
	}
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", append(args, call...)...)
	cmd.Args[0] = busctlPath
	cmd.ExtraFiles = []*os.File{binary}
	cmd.Env = []string{"LANG=C"}
	cmd.Dir = "/"
	cmd.WaitDelay = 100 * time.Millisecond
	var output boundedSystemdOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("systemd D-Bus call failed")
	}
	return output.buffer.Bytes(), nil
}

func verifyProductFiles(unit productUnit, fragment string) bool {
	binary, err := openSystemFile(unit.executable, true)
	if err != nil {
		return false
	}
	binary.Close()

	// /lib may be the root-owned usrmerge alias. Never resolve arbitrary
	// fragment symlinks to executable or transient unit locations.
	if fragment == "/lib/systemd/system/"+unit.name {
		if alias, err := os.Readlink("/lib"); err == nil {
			switch alias {
			case "usr/lib", "/usr/lib":
				fragment = "/usr/lib/systemd/system/" + unit.name
			default:
				return false
			}
		}
	}
	unitFile, err := openSystemFile(fragment, false)
	if err != nil {
		return false
	}
	unitFile.Close()
	return true
}

func trustedBusSocket() bool {
	info, err := os.Lstat(busSocket)
	return err == nil && info.Mode().Type() == os.ModeSocket && rootOwned(info) && trustedParents(busSocket)
}

func openSystemFile(path string, executable bool) (*os.File, error) {
	if !trustedParents(path) {
		return nil, errors.New("untrusted system file parent")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !rootOwned(info) ||
		info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 ||
		(executable && info.Mode().Perm()&0100 == 0) {
		file.Close()
		return nil, errors.New("untrusted system file")
	}
	return file, nil
}

func trustedParents(path string) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !rootOwned(info) || info.Mode().Perm()&0022 != 0 {
			return false
		}
		if dir == "/" {
			return true
		}
	}
}

func rootOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}
