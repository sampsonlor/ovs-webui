#!/usr/bin/env python3
"""Apply the explicit optional correction to an exact pinned disposable tree."""
import hashlib,json,os,subprocess,sys
from pathlib import Path
version,target,archive=sys.argv[1:4]
assert version in ['3.3.9','3.7.1','4.0.0']
here=Path(__file__).resolve().parent/version
target=Path(target).resolve(strict=True)
pin=json.loads((here/'upstream.json').read_text())
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assert sha(Path(archive))==pin['archive_sha256'],'Untrusted upstream archive'
expected=pin['witness_inputs'] if '--with-witness' in sys.argv else pin['inputs']
for name,digest in expected.items():
 p=target/name
 assert not p.is_symlink() and p.resolve().is_relative_to(target) and sha(p)==digest,name
patch=here/'mirror-delete-revalidation.patch'
assert sha(patch)==pin['patch_sha256']
env=dict(os.environ,GIT_CEILING_DIRECTORIES=str(target.parent))
subprocess.run(['git','apply','--check',str(patch)],cwd=target,env=env,check=True)
subprocess.run(['git','apply',str(patch)],cwd=target,env=env,check=True)
print('Applied optional Mirror unregister correction; disabled unless explicitly configured')
