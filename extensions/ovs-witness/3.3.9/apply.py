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
for name, expected in pin["inputs"].items():
    path = target / name
    if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        raise SystemExit("Unexpected upstream input: " + name)
git_env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(target.parent))
subprocess.run(["git", "apply", "--check", str(here / "witness.patch")], cwd=target, env=git_env, check=True)
subprocess.run(["git", "apply", str(here / "witness.patch")], cwd=target, env=git_env, check=True)
for path in (here / "overlay").rglob("*"):
    if path.is_file():
        dest = target / path.relative_to(here / "overlay")
        if dest.exists() or not dest.resolve().is_relative_to(target):
            raise SystemExit("Overlay collision: " + str(dest))
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, dest)
print("Applied optional read-only witness to OVS " + pin["commit"])
