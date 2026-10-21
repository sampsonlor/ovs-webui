#!/usr/bin/env bash
set -euo pipefail
repo=$(pwd)
out="$repo/outputs/ovs-witness-ci"
mkdir -p "$out" test-results/ovs-witness
test "$(uname -m)" = "${EXPECTED_MACHINE:?}"
commit=1a3fefbcd0b0f70ced621b92e286fef3dffaac09
curl --fail --location --retry 0 --max-time 90 \
  "https://api.github.com/repos/openvswitch/ovs/tarball/$commit" -o "$out/upstream.tar.gz"
mkdir "$out/source"
tar -xzf "$out/upstream.tar.gz" --strip-components=1 -C "$out/source"
python3 extensions/ovs-witness/3.3.9/apply.py "$out/source"
cd "$out/source"
./boot.sh > "$repo/test-results/ovs-witness/bootstrap.log" 2>&1
for mode in default enabled; do
  mkdir "$out/$mode"
  cd "$out/$mode"
  flags=()
  if [ "$mode" = enabled ]; then flags+=(--enable-ovs-webui-witness); fi
  "$out/source/configure" --enable-ssl --disable-libcapng \
    --prefix="$out/install-$mode" "${flags[@]}" \
    CFLAGS='-O1 -g -Werror' > "$repo/test-results/ovs-witness/configure-$mode.log" 2>&1
  make -j4 > "$repo/test-results/ovs-witness/build-$mode.log" 2>&1
  cd "$repo"
  sudo -- python3 tests/ovs_witness/prototype.py --build "$out/$mode" \
    --source "$out/source" --mode "$mode" \
    --report "test-results/ovs-witness/native-$mode.json"
done
