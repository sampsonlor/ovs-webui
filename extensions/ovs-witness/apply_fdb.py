#!/usr/bin/env python3
"""Apply the bounded FDB addition to an exact, disposable STP+Mirror tree."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify(path, digest, root):
    if path.is_symlink() or not path.resolve().is_relative_to(root) or sha(path) != digest:
        raise SystemExit("Unexpected pinned FDB build input: " + str(path))


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in {"3.3.9", "3.7.1", "4.0.0"}:
        raise SystemExit("usage: apply_fdb.py <3.3.9|3.7.1|4.0.0> <disposable-source>")
    version = sys.argv[1]
    target = Path(sys.argv[2]).resolve(strict=True)
    if not target.is_dir() or target == REPO or (target / ".git").exists():
        raise SystemExit("FDB patch requires disposable extracted source")
    profiles = REPO / "internal/ovswitness"
    profile = json.loads((REPO / "internal/fdbbuild" / f"build-profile-fdb-{version}.json").read_text(encoding="utf-8"))
    parent = profiles / f"build-profile-mirror-{version}.json"
    verify(parent, profile["parent_profile_sha256"], REPO)
    for name, digest in profile["witness_inputs"].items():
        verify(target / name, digest, target)
    for name, digest in profile["files"].items():
        verify(REPO / name, digest, REPO)

    overlay_root = HERE / "fdb-overlay"
    overlay = [(source, target / source.relative_to(overlay_root))
               for source in overlay_root.rglob("*") if source.is_file()]
    for source, dest in overlay:
        if (source.relative_to(REPO).as_posix() not in profile["files"]
                or dest.exists() or dest.is_symlink() or not dest.resolve().is_relative_to(target)):
            raise SystemExit("Unpinned FDB overlay or destination collision: " + str(dest))

    patch = HERE / version / "fdb.patch"
    git_env = dict(os.environ, GIT_CEILING_DIRECTORIES=str(target.parent))
    subprocess.run(["git", "apply", "--check", str(patch)], cwd=target, env=git_env, check=True)
    subprocess.run(["git", "apply", str(patch)], cwd=target, env=git_env, check=True)
    for source, dest in overlay:
        shutil.copyfile(source, dest)
    print("Applied optional bounded FDB observation to OVS " + version)


if __name__ == "__main__":
    main()
