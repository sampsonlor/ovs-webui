//go:build linux

package fieldexecution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/mirror"
	"github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

// The Mirror source gate proves the daemon's actual connection to the DB peer.
// Run the trusted fault transport's frontend in a distinct process so its DB
// peer PID cannot be confused with this test's Unix control client PID. The
// frontend holds the actual database file. No production source check is
// replaced, and all payloads still reach the real isolated ovsdb-server.
func (f *fixture) startMirrorRelay() {
	f.t.Helper()
	upstream := filepath.Join(f.root, "fault-upstream.sock")
	f.proxy = newProxy(f.t, upstream, f.dbSocket)
	f.proxy.markerOnly.Store(true)
	script := `import os,socket,sys,threading
path,target,db=sys.argv[1:]
held=open(db,'rb')
listener=socket.socket(socket.AF_UNIX);listener.bind(path);listener.listen(32)
def serve(c):
 u=socket.socket(socket.AF_UNIX);u.connect(target)
 def pump(a,b):
  try:
   while True:
    data=a.recv(65536)
    if not data:break
    b.sendall(data)
  except OSError:pass
  finally:
   for s in [a,b]:
    try:s.shutdown(socket.SHUT_RDWR)
    except OSError:pass
 t=threading.Thread(target=pump,args=(c,u),daemon=True);t.start();pump(u,c);t.join();c.close();u.close()
while True:
 c,_=listener.accept();threading.Thread(target=serve,args=(c,),daemon=True).start()
`
	cmd := exec.Command("python3", "-u", "-c", script, f.proxySocket, upstream, f.conf)
	must(f.t, cmd.Start())
	f.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitFor(f.t, func() bool { _, err := os.Stat(f.proxySocket); return err == nil })
}

func (f *fixture) checkMirrorRuntimeRejections(object candidate.Binding, desired mirror.Configuration) {
	f.t.Helper()
	f.discardPortDraft()
	in := f.prepareTopology(object, candidate.MirrorConfigure, candidate.TopologyRequest{Mirror: &desired})
	exe, err := exec.LookPath("ovs-vswitchd")
	must(f.t, err)
	exe, err = filepath.Abs(exe)
	must(f.t, err)
	manifest := filepath.Join(f.root, "mirror-build.json")
	data, err := os.ReadFile(manifest)
	must(f.t, err)
	digest := sha256.Sum256(data)
	good := ovsdb.MirrorExecutionOptions{ControlSocket: filepath.Join(f.root, "switch.ctl"), Executable: exe, Manifest: manifest, ManifestDigest: hex.EncodeToString(digest[:]), PeerUID: uint32(os.Geteuid())}
	wrongProfile := map[string]string{}
	must(f.t, json.Unmarshal(data, &wrongProfile))
	wrongProfile["profile_sha256"] = strings.Repeat("0", 64)
	wrongData, err := json.Marshal(wrongProfile)
	must(f.t, err)
	wrongPath := filepath.Join(f.root, "mirror-unqualified-build.json")
	must(f.t, os.WriteFile(wrongPath, wrongData, 0600))
	wrongDigest := sha256.Sum256(wrongData)
	for _, name := range []string{"missing", "wrong-manifest-digest", "wrong-executable", "unknown-profile"} {
		p, err := ovsdb.New(ovsdb.Options{Socket: f.proxySocket, DatabaseFile: f.conf, PeerUID: uint32(os.Geteuid())})
		must(f.t, err)
		o := good
		switch name {
		case "wrong-manifest-digest":
			o.ManifestDigest = strings.Repeat("0", 64)
		case "wrong-executable":
			o.Executable, err = os.Executable()
			must(f.t, err)
		case "unknown-profile":
			o.Manifest, o.ManifestDigest = wrongPath, hex.EncodeToString(wrongDigest[:])
		}
		if name != "missing" {
			must(f.t, p.ConfigureMirrorExecution(o))
		}
		if _, err = p.Executor(f.inventory).Prepare(f.ctx, repository.NewID(), strings.Repeat("b", 64), in.Envelope); err == nil {
			f.t.Fatal("unqualified Mirror runtime prepared a mutation", name)
		}
	}
	// A root pin becoming writable inside the admission callback must be caught
	// by the final check before the single native send, with no OVS mutation.
	plan, err := f.executor.Prepare(f.ctx, repository.NewID(), strings.Repeat("a", 64), in.Envelope)
	must(f.t, err)
	sent, before := f.proxy.sent.Load(), f.vs("get", "Mirror", object.OVSUUID, "select_vlan")
	out := f.executor.Commit(f.ctx, plan, func() error { return os.Chmod(manifest, 0666) })
	if out.Commit != "rejected" || out.Reason != "mirror-runtime-unqualified" || f.proxy.sent.Load() != sent || f.vs("get", "Mirror", object.OVSUUID, "select_vlan") != before {
		f.t.Fatal("late build revocation dispatched or invented a mutation", out)
	}
	// A retained observation socket must not retain build trust. The next
	// actual monitor publication must withdraw new-write availability too.
	waitFor(f.t, func() bool { return f.topologySnapshot().Topology.MirrorRuntime == "" })
}

func (f *fixture) configureMirrorBuild() {
	f.t.Helper()
	profile := os.Getenv("OVS_MIRROR_BUILD_PROFILE")
	if profile == "" || f.mirrorBuildRevoked {
		return
	}
	variant := os.Getenv("OVS_MIRROR_BUILD_VARIANT")
	if variant == "" {
		variant = "fixed"
	}
	executable, err := exec.LookPath("ovs-vswitchd")
	must(f.t, err)
	executable, err = filepath.Abs(executable)
	must(f.t, err)
	exe, err := os.ReadFile(executable)
	must(f.t, err)
	h := sha256.Sum256(exe)
	manifest := map[string]string{"format": "ovs-webui.mirror-build.v1", "profile_sha256": profile, "architecture": runtime.GOARCH, "variant": variant, "executable_sha256": hex.EncodeToString(h[:])}
	data, err := json.Marshal(manifest)
	must(f.t, err)
	path := filepath.Join(f.root, "mirror-build.json")
	if prior, err := os.ReadFile(path); os.IsNotExist(err) {
		must(f.t, os.WriteFile(path, data, 0600))
	} else if err != nil || string(prior) != string(data) {
		f.t.Fatal("fixed Mirror build pin changed across manager reopen")
	}
	h = sha256.Sum256(data)
	must(f.t, f.provider.ConfigureMirrorExecution(ovsdb.MirrorExecutionOptions{ControlSocket: filepath.Join(f.root, "switch.ctl"), Executable: executable, Manifest: path, ManifestDigest: hex.EncodeToString(h[:]), PeerUID: uint32(os.Geteuid())}))
}
