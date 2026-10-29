#!/usr/bin/env python3
"""Verify both independent profile components without replacing old STP pins."""
import hashlib,json
from pathlib import Path
sha=lambda p:hashlib.sha256(p.read_text(encoding="utf-8").encode()).hexdigest()
for v in ['3.3.9','3.7.1','4.0.0']:
 base=Path('extensions/ovs-mirror-delete')/v
 pin=json.loads((base/'upstream.json').read_text())
 profile=json.loads((base/'build-profile.json').read_text())
 assert profile['format']=='ovs-webui.mirror-build-profile.v1'
 assert profile['patch_sha256']==pin['patch_sha256']==sha(base/'mirror-delete-revalidation.patch')
 assert profile['inputs']==pin['inputs'] and profile['witness_inputs']==pin['witness_inputs']
 assert profile['upstream_commit']==pin['commit'] and profile['archive_sha256']==pin['archive_sha256']
 assert (base/'build-profile.json').read_bytes()==(Path('internal/mirror')/('build-profile-'+v+'.json')).read_bytes()
 old=Path('internal/ovswitness')/('build-profile.json' if v=='3.3.9' else 'build-profile-'+v+'.json')
 assert profile['witness_base_profile_sha256']==sha(old)
 combined=json.loads((Path('internal/ovswitness')/('build-profile-mirror-'+v+'.json')).read_text())
 assert combined['parent_profile_sha256']==sha(old) and combined['native_mirror_profile_sha256']==sha(base/'build-profile.json')
 for name,digest in combined['files'].items():assert sha(Path(name))==digest,name
 assert combined['configure']==json.loads(old.read_text())['configure']+['--enable-ovs-webui-mirror-fix']
print('Three exact optional Mirror profiles and preserved STP parent profiles verified')
