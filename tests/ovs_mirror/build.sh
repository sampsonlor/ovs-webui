#!/usr/bin/env bash
set -euo pipefail
repo=$(pwd)
version=3.3.9
out="$repo/outputs/ovs-mirror-ci"
mkdir -p "$out" "$repo/test-results/mirror-build"
commit=$(python3 -c 'import json;print(json.load(open("extensions/ovs-mirror-delete/3.3.9/upstream.json"))["commit"])')
curl --fail --location --retry 0 --max-time 90 "https://api.github.com/repos/openvswitch/ovs/tarball/$commit" -o "$out/upstream.tar.gz"
python3 - "$out/upstream.tar.gz" <<'PY'
import hashlib,json,sys
from pathlib import Path
p=json.loads(Path('extensions/ovs-mirror-delete/3.3.9/upstream.json').read_text())
assert hashlib.sha256(Path(sys.argv[1]).read_bytes()).hexdigest()==p['archive_sha256']
PY
mkdir "$out/source" "$out/fixed"
tar -xzf "$out/upstream.tar.gz" --strip-components=1 -C "$out/source"
python3 extensions/ovs-mirror-delete/apply.py "$version" "$out/source" "$out/upstream.tar.gz"
python3 tests/ovs_mirror/check_profiles.py
cd "$out/source"
./boot.sh > "$repo/test-results/mirror-build/bootstrap.log" 2>&1
cd "$out/fixed"
"$out/source/configure" --enable-ssl --disable-libcapng --disable-shared --enable-static \
  --enable-ovs-webui-mirror-fix --prefix="$out/install-fixed" \
  CFLAGS='-O1 -g -Werror' LIBS='-lm' > "$repo/test-results/mirror-build/configure.log" 2>&1
make -j4 > "$repo/test-results/mirror-build/build.log" 2>&1
cd "$repo"
python3 - "$out" <<'PY'
import hashlib,json,os,sys
from pathlib import Path
out=Path(sys.argv[1]);p=Path('extensions/ovs-mirror-delete/3.3.9/build-profile.json')
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
result={'status':'isolated-build-not-production-release','source_commit':os.environ.get('MIRROR_SOURCE_COMMIT') or os.environ.get('GITHUB_SHA'),
        'profile_sha256':sha(p),'patch_sha256':sha(Path('extensions/ovs-mirror-delete/3.3.9/mirror-delete-revalidation.patch')),
        'architecture':os.uname().machine,'variant':'fixed',
        'executables':{n:sha(out/'fixed'/folder/n) for n,folder in [('ovs-vswitchd','vswitchd'),('ovsdb-server','ovsdb')]}}
Path('test-results/mirror-build/manifest.json').write_text(json.dumps(result,indent=2)+'\n')
PY

sudo install -d -m 0755 "$out/bin"
for binary in ovs-vswitchd ovsdb-server ovsdb-tool ovs-vsctl ovsdb-client ovs-appctl; do
  case "$binary" in ovsdb-*) folder=ovsdb ;; ovs-vswitchd) folder=vswitchd ;; *) folder=utilities ;; esac
  sudo install -m 0755 "$out/fixed/$folder/$binary" "$out/bin/$binary"
done
