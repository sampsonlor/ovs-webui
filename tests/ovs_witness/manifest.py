#!/usr/bin/env python3
"""Build provenance only; this is not a signed production trust manifest."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

out = Path(sys.argv[1])
extension = Path("extensions/ovs-witness/3.3.9")
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
result = {
    "status": "isolated-prototype-not-a-production-release",
    "checked_out_commit": os.environ.get("GITHUB_SHA") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
    "source_commit": os.environ.get("WITNESS_SOURCE_COMMIT") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
    "upstream": json.loads((extension / "upstream.json").read_text()),
    "architecture": os.uname().machine,
    "witness_abi": "ovs-webui.stp-witness.v1",
    "patch_sha256": sha(extension / "witness.patch"),
    "overlay_sha256": {str(p.relative_to(extension)): sha(p) for p in sorted((extension / "overlay").rglob("*")) if p.is_file()},
    "executables": {mode: sha(out / mode / "vswitchd/ovs-vswitchd") for mode in ["default", "enabled", "sanitized"]},
    "compiler": subprocess.check_output(["gcc", "--version"], text=True).splitlines()[0],
    "configure_common": ["--enable-ssl", "--disable-libcapng", "--disable-shared", "--enable-static"],
    "libs": "-lm",
    "sanitizer": {"address": True, "undefined": True, "unit_leaks": True, "native_daemon_exit_leaks": False, "thread_sanitizer": False},
    "native_results": {mode: json.loads(Path("test-results/ovs-witness/native-" + mode + ".json").read_text()) for mode in ["default", "enabled", "sanitized"]},
}
assert all(r["status"] == "pass" for r in result["native_results"].values())
Path("test-results/ovs-witness/build-manifest.json").write_text(json.dumps(result, indent=2) + "\n")
