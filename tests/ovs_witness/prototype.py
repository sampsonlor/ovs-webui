#!/usr/bin/env python3
"""Disposable kernel OVS fixture; never operates on the host's OVS service."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import uuid


def control(path, method, args):
    with socket.socket(socket.AF_UNIX) as sock:
        sock.settimeout(2)
        sock.connect(str(path))
        sock.sendall(json.dumps({"method": method, "params": args, "id": 1}).encode() + b"\n")
        data = b""
        while len(data) <= 65536:
            data += sock.recv(min(4096, 65537 - len(data)))
            try:
                value = json.loads(data)
                assert value["id"] == 1
                return value
            except json.JSONDecodeError:
                continue
        raise AssertionError("wire budget")


class Fixture:
    def __init__(self, build, source):
        self.build, self.source = Path(build).resolve(), Path(source).resolve()
        self.temp = tempfile.TemporaryDirectory(prefix="ow-")
        self.root = Path(self.temp.name)
        self.ns = "ow-" + uuid.uuid4().hex[:10]
        self.procs, self.logs = [], []
        self.env = dict(os.environ, OVS_RUNDIR=str(self.root), OVS_LOGDIR=str(self.root),
                        OVS_DBDIR=str(self.root), OVS_SYSCONFDIR=str(self.root))

    def tool(self, name):
        return str(self.build / ("ovsdb" if name.startswith("ovsdb-") else
                                 "vswitchd" if name == "ovs-vswitchd" else "utilities") / name)

    def run(self, name, *args):
        return subprocess.check_output(["ip", "netns", "exec", self.ns, self.tool(name), *args],
                                       env=self.env, stderr=subprocess.PIPE, timeout=10).decode().strip()

    def vsctl(self, *args):
        return self.run("ovs-vsctl", "--timeout=5", "--db=unix:" + str(self.root / "db.sock"), *args)

    def start(self, name, *args):
        log = open(self.root / (name + ".log"), "ab")
        self.logs.append(log)
        proc = subprocess.Popen(["ip", "netns", "exec", self.ns, self.tool(name), *args],
                                env=self.env, stdout=log, stderr=log)
        self.procs.append(proc)
        return proc

    def __enter__(self):
        subprocess.run(["ip", "netns", "add", self.ns], check=True)
        subprocess.run(["ip", "netns", "exec", self.ns, "ip", "link", "set", "lo", "up"], check=True)
        self.run("ovsdb-tool", "create", str(self.root / "conf.db"), str(self.source / "vswitchd/vswitch.ovsschema"))
        self.server = self.start("ovsdb-server", "--no-chdir", "--unixctl=" + str(self.root / "db.ctl"),
                                 "--remote=punix:" + str(self.root / "db.sock"), str(self.root / "conf.db"))
        deadline = time.monotonic() + 5
        while not (self.root / "db.sock").exists():
            assert self.server.poll() is None and time.monotonic() < deadline
            time.sleep(.02)
        self.vsctl("--no-wait", "init")
        self.vswitchd = self.start("ovs-vswitchd", "--no-chdir", "--unixctl=" + str(self.root / "vsd.ctl"),
                                  "unix:" + str(self.root / "db.sock"))
        self.vsctl("add-br", "br-witness", "--", "set", "Bridge", "br-witness", "stp_enable=true")
        self.bridge_uuid = self.vsctl("get", "Bridge", "br-witness", "_uuid")
        return self

    def witness(self, bridge=None):
        nonce = os.urandom(32).hex()
        outer = control(self.root / "vsd.ctl", "ovs-webui/stp-witness-v1", [bridge or self.bridge_uuid, nonce])
        assert outer.get("error") is None, outer
        value = json.loads(outer["result"])
        assert value["nonce"] == nonce and value["schema_version"] == "ovs-webui.stp-witness.v1"
        if value["state"] == "withheld":
            assert set(value) == {"nonce", "schema_version", "state", "reason"}
        return value

    def __exit__(self, *_):
        for proc in reversed(self.procs):
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(5)
        for log in self.logs:
            log.close()
        for p in self.root.glob("*.log"):
            print(p.name + ":\n" + p.read_text(errors="replace")[-12000:])
        subprocess.run(["ip", "netns", "del", self.ns], check=True)
        self.temp.cleanup()


def main():
    parser = argparse.ArgumentParser()
    for name in ["build", "source", "mode", "report"]:
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    subprocess.run(["modprobe", "openvswitch"], check=True)
    report = {"mode": args.mode, "cases": [], "runtime": "3.3.9", "architecture": os.uname().machine}
    try:
        with Fixture(args.build, args.source) as f:
            listing = control(f.root / "vsd.ctl", "list-commands", [])["result"]
            if args.mode == "default":
                assert "ovs-webui/" not in listing
                report["cases"].append({"name": "default_build_has_no_extension_command", "status": "pass"})
            else:
                value = f.witness()
                assert value["state"] == "known", value
                assert value["bridge"]["ovs_uuid"] == f.bridge_uuid
                assert value["native"]["stp"]["enabled"] and not value["native"]["rstp"]["enabled"]
                assert value["native"]["stp"]["local"]["hello_time_ms"] == 2000
                report["sample"] = value
                report["cases"].append({"name": "kernel_stp_identity_and_local_snapshot", "status": "pass"})
                f.vsctl("set", "Bridge", "br-witness", "stp_enable=false", "rstp_enable=true")
                value = f.witness()
                assert value["state"] == "known", value
                assert value["native"]["rstp"]["enabled"] and not value["native"]["stp"]["enabled"]
                report["cases"].append({"name": "kernel_rstp_exclusive_local_snapshot", "status": "pass"})
        report["status"] = "pass"
    except BaseException as e:
        report["status"] = "fail"
        report["error"] = repr(e)
        raise
    finally:
        Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
