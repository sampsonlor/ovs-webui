#!/usr/bin/env python3
"""Disposable kernel OVS fixture; never operates on the host's OVS service."""
import argparse
import json
import hashlib
import os
from pathlib import Path
import socket
import shutil
import signal
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
            chunk = sock.recv(min(4096, 65537 - len(data)))
            assert chunk, "truncated control reply"
            data += chunk
            try:
                value = json.loads(data)
                assert value["id"] == 1
                return value
            except json.JSONDecodeError:
                continue
        raise AssertionError("wire budget")


def members(value):
    return value[1] if isinstance(value, list) and value[0] == "set" else [value]


class Fixture:
    def __init__(self, build, source, decoder=None):
        self.build, self.source = Path(build).resolve(), Path(source).resolve()
        self.temp = tempfile.TemporaryDirectory(prefix="ow-")
        self.root = Path(self.temp.name)
        self.ns = "ow-" + uuid.uuid4().hex[:10]
        self.procs, self.logs = [], []
        self.decoder = decoder
        self.manifest = {}
        self.bin = self.root / "bin"
        self.bin.mkdir(mode=0o700)
        for name in ["ovs-vswitchd", "ovsdb-server", "ovsdb-tool", "ovs-vsctl", "ovsdb-client", "ovs-appctl"]:
            folder = "ovsdb" if name.startswith("ovsdb-") else "vswitchd" if name == "ovs-vswitchd" else "utilities"
            dest = self.bin / name
            shutil.copyfile(self.build / folder / name, dest)
            dest.chmod(0o755)
            self.manifest[name] = hashlib.sha256(dest.read_bytes()).hexdigest()
        manifest = self.root / "build-manifest.json"
        manifest.write_text(json.dumps(self.manifest), encoding="utf-8")
        manifest.chmod(0o600)
        self.manifest_path = manifest
        self.env = dict(os.environ, OVS_RUNDIR=str(self.root), OVS_LOGDIR=str(self.root),
                        OVS_DBDIR=str(self.root), OVS_SYSCONFDIR=str(self.root))

    def tool(self, name):
        return str(self.bin / name)

    def network(self, *args):
        return subprocess.check_output(["ip", "netns", "exec", self.ns, "ip", *args], timeout=5)

    def transaction(self, ops):
        value = json.loads(self.run("ovsdb-client", "transact", "unix:" + str(self.root / "db.sock"),
                                    json.dumps(["Open_vSwitch", *ops])))
        assert all("error" not in result for result in value), value
        return value

    def independent_inputs(self):
        columns = json.loads(Path("extensions/ovs-witness/3.3.9/input-columns.json").read_text())
        operations = [{"op": "select", "table": table, "where": [],
                       "columns": ["_uuid", *names, *(["ofport"] if table == "Interface" else [])]}
                      for table, names in columns.items()]
        result = self.transaction(operations)
        rows = {table: reply["rows"] for table, reply in zip(columns, result)}
        rows["Bridge"] = [r for r in rows["Bridge"] if r["_uuid"][1] == self.bridge_uuid]
        assert len(rows["Bridge"]) == 1
        ports = {r[1] for r in members(rows["Bridge"][0]["ports"])}
        rows["Port"] = [r for r in rows["Port"] if r["_uuid"][1] in ports]
        interfaces = {r[1] for p in rows["Port"] for r in members(p["interfaces"])}
        rows["Interface"] = [r for r in rows["Interface"] if r["_uuid"][1] in interfaces]
        assert len(rows["Port"]) == len(ports) and len(rows["Interface"]) == len(interfaces)
        return rows

    def attest(self):
        # No cached path hash: hash the opened actual peer executable FD and
        # check identity/content on every isolated test sample. Production
        # startup caching and its budget remain a later integration decision.
        manifest_stat = self.manifest_path.stat()
        assert manifest_stat.st_uid == 0 and manifest_stat.st_mode & 0o022 == 0
        manifest = json.loads(self.manifest_path.read_text())
        with socket.socket(socket.AF_UNIX) as sock:
            sock.connect(str(self.root / "vsd.ctl"))
            import struct
            pid, uid, _ = struct.unpack("3i", sock.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            assert uid == 0 and pid == self.vswitchd.pid
        with open("/proc/" + str(pid) + "/exe", "rb") as exe:
            before = os.fstat(exe.fileno())
            expected = os.stat(self.tool("ovs-vswitchd"))
            assert before.st_uid == 0 and before.st_mode & 0o022 == 0
            assert (before.st_dev, before.st_ino) == (expected.st_dev, expected.st_ino)
            digest = hashlib.file_digest(exe, "sha256").hexdigest()
            after = os.fstat(exe.fileno())
            fields = ("st_dev", "st_ino", "st_size", "st_mtime_ns", "st_ctime_ns", "st_mode", "st_uid", "st_gid")
            assert all(getattr(before, k) == getattr(after, k) for k in fields)
            assert digest == manifest["ovs-vswitchd"]
        return digest

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
        try:
            return self.setup()
        except BaseException:
            self.__exit__()
            raise

    def setup(self):
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
        self.attest()
        nonce = os.urandom(32).hex()
        outer = control(self.root / "vsd.ctl", "ovs-webui/stp-witness-v1", [bridge or self.bridge_uuid, nonce])
        assert outer.get("error") is None, outer
        value = json.loads(outer["result"])
        assert value["nonce"] == nonce and value["schema_version"] == "ovs-webui.stp-witness.v1"
        if value["state"] == "withheld":
            assert set(value) == {"nonce", "schema_version", "state", "reason"}
        if self.decoder:
            checked = subprocess.run([self.decoder, "--nonce", nonce, "--bridge", bridge or self.bridge_uuid],
                                     input=json.dumps(outer).encode(), capture_output=True, timeout=5)
            if value["state"] == "known":
                assert checked.returncode == 0, checked.stderr.decode()
                assert json.loads(checked.stdout) == value
                inputs = self.independent_inputs()
                hashed = subprocess.run([self.decoder, "--input"], input=json.dumps(inputs).encode(),
                                        capture_output=True, timeout=5)
                assert hashed.returncode == 0, hashed.stderr.decode()
                assert hashed.stdout.decode().strip() == value["configuration"]["runtime_input_digest"], value
            else:
                assert checked.returncode != 0 and not checked.stdout
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
        clean = True
        for p in self.root.glob("*.log"):
            text = p.read_text(errors="replace")
            print(p.name + ":\n" + text[-12000:])
            clean &= "ERROR: AddressSanitizer" not in text and "runtime error:" not in text
        subprocess.run(["ip", "netns", "del", self.ns], check=True)
        self.temp.cleanup()
        assert clean, "native sanitizer error"


def extended(f, report):
    def case(name, check):
        check()
        report["cases"].append({"name": name, "status": "pass"})
        print("PASS " + name, flush=True)

    def known():
        value = f.witness()
        assert value["state"] == "known", value
        return value

    def withheld(reason=None, bridge=None):
        value = f.witness(bridge)
        assert value["state"] == "withheld", value
        if reason:
            assert value["reason"] == reason, value
        return value

    def wait_fixture(predicate, seconds=10):
        # Setup only: wait for an explicitly induced native state, never retry
        # an acceptance assertion or the production provider's failed call.
        deadline = time.monotonic() + seconds
        while not predicate():
            assert time.monotonic() < deadline, "fixture state did not arrive"
            time.sleep(.05)

    def args_rejected():
        for params in [[f.bridge_uuid], [f.bridge_uuid, "f" * 64, "extra"],
                       ["br-witness", "f" * 64], [f.bridge_uuid, "F" * 64],
                       [f.bridge_uuid, "f" * 63], ["/tmp/socket", "f" * 64]]:
            assert control(f.root / "vsd.ctl", "ovs-webui/stp-witness-v1", params).get("error") is not None
    case("fixed_uuid_nonce_and_exact_arity", args_rejected)
    case("foreign_uuid_has_no_partial_evidence", lambda: withheld("BRIDGE_NOT_CONFIGURED", str(uuid.uuid4())))

    def invalid_build():
        original = f.manifest_path.read_bytes()
        bad = dict(f.manifest, **{"ovs-vswitchd": "0" * 64})
        f.manifest_path.write_text(json.dumps(bad))
        try:
            f.attest()
        except AssertionError:
            pass
        else:
            raise AssertionError("wrong actual executable hash was trusted")
        finally:
            f.manifest_path.write_bytes(original)
        f.manifest_path.chmod(0o666)
        try:
            f.attest()
        except AssertionError:
            pass
        else:
            raise AssertionError("writable manifest was trusted")
        finally:
            f.manifest_path.chmod(0o600)
        f.attest()
    case("actual_peer_executable_hash_and_manifest_permissions_are_required", invalid_build)

    def no_side_effects():
        before = known()
        inputs = f.independent_inputs()
        for _ in range(12):
            after = known()
            for key in ["bridge", "database", "configuration", "native", "daemon_instance_id"]:
                assert before[key] == after[key], key
        assert inputs == f.independent_inputs()
        report["independent_input_image"] = inputs
    case("repeat_reads_preserve_configuration_epochs_and_native_local", no_side_effects)

    def raw_values():
        f.vsctl("set", "Bridge", "br-witness", "stp_enable=true", "rstp_enable=false",
                "other_config:stp-priority=4096", "other_config:rstp-priority=8192")
        value = known()
        p = value["configuration"]["source"]["parameters"]
        assert p["stp-priority"] == "4096" and p["rstp-priority"] == "8192"
        assert value["native"]["stp"]["local"]["priority"] == 4096
        f.vsctl("remove", "Bridge", "br-witness", "other_config", "stp-priority", "rstp-priority")
        assert known()["configuration"]["source"]["parameters"]["stp-priority"] is None
    case("raw_active_inactive_and_unset_values_are_preserved", raw_values)

    def unicode_digest():
        f.vsctl("set", "Bridge", "br-witness", 'other_config:witness-opaque="a:b,雪\\nvalue"')
        known()  # Each known call independently compares the actual C/Go hash.
        f.vsctl("remove", "Bridge", "br-witness", "other_config", "witness-opaque")
        known()
    case("c_go_typed_digest_includes_unicode_and_opaque_map_values", unicode_digest)

    def budget():
        f.vsctl("set", "Bridge", "br-witness", "other_config:stp-priority=" + "1" * 129)
        withheld("WITNESS_BUDGET")
        f.vsctl("remove", "Bridge", "br-witness", "other_config", "stp-priority")
        known()
    case("oversized_raw_parameter_withholds_all_fields", budget)

    def unit_caveat():
        f.vsctl("set", "Bridge", "br-witness", "other_config:stp-hello-time=2")
        value = known()
        assert value["configuration"]["source"]["parameters"]["stp-hello-time"] == "2"
        assert value["native"]["stp"]["local"]["hello_time_ms"] == 1000
        f.vsctl("remove", "Bridge", "br-witness", "other_config", "stp-hello-time")
        assert known()["native"]["stp"]["local"]["hello_time_ms"] == 2000
    case("native_stp_unit_caveat_is_reported_without_correction", unit_caveat)

    def external_reference():
        f.vsctl("--", "--id=@m", "create", "Mirror", "name=witness-mirror",
                "--", "set", "Bridge", "br-witness", "mirrors=@m")
        withheld("BINDING_UNPROVEN")
        f.vsctl("clear", "Bridge", "br-witness", "mirrors")
        known()
    case("unmodelled_table_reference_is_withheld", external_reference)

    def same_name_uuid():
        before = known()
        old = f.bridge_uuid
        columns = json.loads(Path("extensions/ovs-witness/3.3.9/input-columns.json").read_text())["Bridge"]
        rows = f.transaction([{"op": "select", "table": "Bridge", "where": [["_uuid", "==", ["uuid", old]]], "columns": columns},
                              {"op": "select", "table": "Open_vSwitch", "where": [], "columns": ["_uuid", "next_cfg"]}])
        root = rows[1]["rows"][0]
        next_cfg = root["next_cfg"] + 1
        result = f.transaction([
            {"op": "insert", "table": "Bridge", "uuid-name": "new_bridge", "row": rows[0]["rows"][0]},
            {"op": "mutate", "table": "Open_vSwitch", "where": [], "mutations": [
                ["bridges", "delete", ["set", [["uuid", old]]]],
                ["bridges", "insert", ["set", [["named-uuid", "new_bridge"]]]]]},
            {"op": "delete", "table": "Bridge", "where": [["_uuid", "==", ["uuid", old]]]},
            {"op": "update", "table": "Open_vSwitch", "where": [], "row": {"next_cfg": next_cfg}}])
        f.bridge_uuid = result[0]["uuid"][1]
        wait_fixture(lambda: int(f.vsctl("get", "Open_vSwitch", ".", "cur_cfg")) == next_cfg)
        after = known()
        assert after["bridge"]["ovs_uuid"] != old
        assert after["bridge"]["runtime_instance_id"] == before["bridge"]["runtime_instance_id"], "fixture must exercise real runtime reuse"
        assert int(after["bridge"]["binding_epoch"]) > int(before["bridge"]["binding_epoch"])
        withheld("BRIDGE_NOT_CONFIGURED", old)
    case("same_name_new_uuid_invalidates_binding_even_when_runtime_is_reused", same_name_uuid)

    def settled_aba():
        before = known()
        f.vsctl("set", "Bridge", "br-witness", "other_config:stp-priority=4096")
        middle = known()
        f.vsctl("remove", "Bridge", "br-witness", "other_config", "stp-priority")
        after = known()
        assert before["native"] == after["native"] and middle["native"] != after["native"]
        assert int(after["configuration"]["epoch"]) > int(before["configuration"]["epoch"])
    case("processed_configuration_ABA_advances_epoch", settled_aba)

    def non_alert_input():
        before = known()
        row = f.transaction([{"op": "select", "table": "Port", "where": [["name", "==", "br-witness"]], "columns": ["_uuid"]}])[0]["rows"][0]
        f.transaction([{"op": "update", "table": "Port", "where": [["_uuid", "==", row["_uuid"]]], "row": {"trunks": ["set", [7, 9]]}}])
        after = f.witness()
        if after["state"] == "known":
            assert int(after["configuration"]["epoch"]) > int(before["configuration"]["epoch"])
            assert after["configuration"]["runtime_input_digest"] != before["configuration"]["runtime_input_digest"]
        f.transaction([{"op": "update", "table": "Port", "where": [["_uuid", "==", row["_uuid"]]], "row": {"trunks": ["set", []]}}])
        f.vsctl("sync")
        known()
    case("omit_alert_input_change_cannot_reuse_old_configuration_evidence", non_alert_input)

    def both_config_flags():
        f.vsctl("set", "Bridge", "br-witness", "stp_enable=true", "rstp_enable=true")
        value = known()
        assert value["configuration"]["source"]["stp_enable"] and value["configuration"]["source"]["rstp_enable"]
        assert not value["native"]["stp"]["enabled"] and value["native"]["rstp"]["enabled"]
        if f.decoder:
            checked = subprocess.run([f.decoder, "--nonce", value["nonce"], "--bridge", f.bridge_uuid, "--exclusive"],
                                     input=json.dumps({"id": 1, "error": None, "result": json.dumps(value)}).encode(), capture_output=True, timeout=5)
            assert checked.returncode != 0 and not checked.stdout
        f.vsctl("set", "Bridge", "br-witness", "stp_enable=true", "rstp_enable=false")
        known()
    case("both_config_flags_report_native_rstp_precedence_without_authorizing_transition", both_config_flags)

    def disabled():
        f.vsctl("set", "Bridge", "br-witness", "stp_enable=false", "rstp_enable=false")
        value = known()
        assert all(e == {"enabled": False, "local": None} for e in value["native"].values())
        f.vsctl("set", "Bridge", "br-witness", "stp_enable=true")
        known()
    case("both_disabled_are_observed_without_default_local_values", disabled)

    def native_non_root():
        f.network("link", "add", "witness-a", "type", "veth", "peer", "name", "witness-b")
        f.network("link", "set", "witness-a", "up")
        f.network("link", "set", "witness-b", "up")
        f.vsctl("add-br", "br-peer", "--", "add-port", "br-witness", "witness-a",
                "--", "add-port", "br-peer", "witness-b", "--", "set", "Bridge", "br-peer",
                "stp_enable=true", "other_config:stp-priority=4096", "other_config:stp-max-age=12", "other_config:stp-forward-delay=9")
        for protocol in ["stp", "rstp"]:
            if protocol == "rstp":
                f.vsctl("set", "Bridge", "br-witness", "stp_enable=false", "rstp_enable=true", "--",
                        "set", "Bridge", "br-peer", "stp_enable=false", "rstp_enable=true",
                        "other_config:rstp-priority=4096", "other_config:rstp-max-age=12", "other_config:rstp-forward-delay=9")
            def learned():
                text = control(f.root / "vsd.ctl", protocol + "/show", ["br-witness"])["result"]
                root = text.split("Root ID:")[1].split("Bridge ID:")[0]
                timers = text.split("Bridge ID:")[1] if protocol == "stp" else root
                return "This bridge is the root" not in root and "12s" in timers
            wait_fixture(learned, 15)
            value = known()
            local = value["native"][protocol]["local"]
            assert local["max_age_ms"] == 20000 and local["forward_delay_ms"] == 15000
            report["cases"].append({"name": protocol + "_non_root_local_differs_from_learned_timers", "status": "pass"})
    native_non_root()

    def setter_error():
        f.network("link", "add", "witness-c", "type", "veth", "peer", "name", "witness-d")
        f.network("link", "set", "witness-c", "up")
        f.vsctl("add-port", "br-witness", "witness-c", "--",
                "set", "Bridge", "br-witness", "stp_enable=true", "rstp_enable=false", "--",
                "set", "Port", "witness-a", "other_config:stp-port-num=1")
        withheld("SETTER_REPORTED_ERROR")
        f.vsctl("remove", "Port", "witness-a", "other_config", "stp-port-num")
        known()
    case("real_mixed_manual_stp_port_ids_withhold_setter_completion", setter_error)

    def contention():
        before = known()
        with socket.socket(socket.AF_UNIX) as thief:
            thief.settimeout(2)
            thief.connect(str(f.root / "db.sock"))
            thief.sendall(b'{"method":"steal","params":["ovs_vswitchd"],"id":77}\n')
            data = b""
            while len(data) < 65536:
                data += thief.recv(4096)
                try:
                    reply = json.loads(data)
                    break
                except json.JSONDecodeError:
                    continue
            assert reply["result"]["locked"]
            wait_fixture(lambda: "br-witness" not in control(f.root / "vsd.ctl", "dpif/show", [])["result"])
            withheld("IDL_LOCK_CONTENDED")
        # Native IDL re-acquisition recreates the bridge; wait on its standard
        # status surface before making a single acceptance witness call.
        wait_fixture(lambda: "br-witness" in control(f.root / "vsd.ctl", "dpif/show", [])["result"])
        f.vsctl("sync")
        after = known()
        assert after["daemon_instance_id"] == before["daemon_instance_id"]
        assert int(after["database"]["authority_epoch"]) > int(before["database"]["authority_epoch"])
        assert after["bridge"]["runtime_instance_id"] != before["bridge"]["runtime_instance_id"]
    case("actual_lock_steal_and_reacquire_changes_authority_and_runtime", contention)

    def restart_daemon():
        before = known()
        f.vswitchd.terminate()
        f.vswitchd.wait(5)
        f.vswitchd = f.start("ovs-vswitchd", "--no-chdir", "--unixctl=" + str(f.root / "vsd.ctl"),
                             "unix:" + str(f.root / "db.sock"))
        f.vsctl("sync")
        after = known()
        assert after["daemon_instance_id"] != before["daemon_instance_id"]
        assert after["bridge"]["runtime_instance_id"] != before["bridge"]["runtime_instance_id"]
    case("process_restart_does_not_reuse_daemon_or_runtime_instance", restart_daemon)

    def connection_reset():
        before = known()
        f.server.terminate()
        f.server.wait(5)
        states = []
        def disconnected():
            value = f.witness()
            states.append(value["state"] if value["state"] == "known" else value["reason"])
            return value["state"] == "withheld" and value["reason"] == "IDL_NOT_CONNECTED"
        wait_fixture(disconnected)
        withheld("IDL_NOT_CONNECTED")
        f.server = f.start("ovsdb-server", "--no-chdir", "--unixctl=" + str(f.root / "db.ctl"),
                           "--remote=punix:" + str(f.root / "db.sock"), str(f.root / "conf.db"))
        f.vsctl("sync")
        after = known()
        assert after["daemon_instance_id"] == before["daemon_instance_id"]
        assert int(after["database"]["authority_epoch"]) > int(before["database"]["authority_epoch"])
        report["disconnect_fixture_states"] = states
    case("real_database_disconnect_and_reconnect_do_not_reuse_authority_epoch", connection_reset)


def main():
    parser = argparse.ArgumentParser()
    for name in ["build", "source", "mode", "report"]:
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    subprocess.run(["modprobe", "openvswitch"], check=True)
    report = {"mode": args.mode, "cases": [], "runtime": "3.3.9", "architecture": os.uname().machine}
    try:
        decoder = str(Path("outputs/ovs-witness-ci/decoder").resolve())
        with Fixture(args.build, args.source, decoder if Path(decoder).exists() else None) as f:
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
                extended(f, report)
            report["executable_sha256"] = f.attest()
        report["status"] = "pass"
    except BaseException as e:
        report["status"] = "fail"
        report["error"] = repr(e)
        raise
    finally:
        Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
