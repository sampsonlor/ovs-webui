#!/usr/bin/env python3
"""Apply the exact experimental patch and overlay to a disposable OVS tree."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

here = Path(__file__).resolve().parent
target = Path(sys.argv[1]).resolve(strict=True)
pin = json.loads((here / "upstream.json").read_text(encoding="utf-8"))
if len(sys.argv) > 2:
    archive = Path(sys.argv[2]).read_bytes()
    if hashlib.sha256(archive).hexdigest() != pin["archive_sha256"]:
        raise SystemExit("Upstream archive digest mismatch")
for name, expected in pin["inputs"].items():
    path = target / name
    if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        raise SystemExit("Unexpected upstream input: " + name)
overlay = [(p, target / p.relative_to(here / "overlay"))
           for p in (here / "overlay").rglob("*") if p.is_file()]
for _, dest in overlay:
    if dest.exists() or not dest.resolve().is_relative_to(target):
        raise SystemExit("Overlay collision: " + str(dest))
git_env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(target.parent))
subprocess.run(["git", "apply", "--check", str(here / "witness.patch")], cwd=target, env=git_env, check=True)
subprocess.run(["git", "apply", str(here / "witness.patch")], cwd=target, env=git_env, check=True)
for path, dest in overlay:
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(path, dest)
print("Applied optional read-only witness to OVS " + pin["commit"])
