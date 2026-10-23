#!/usr/bin/env bash
set -euo pipefail
repo=$(pwd)
version=${OVS_WITNESS_VERSION:?}
case "$version" in 3.3.9|3.7.1|4.0.0) ;; *) exit 1 ;; esac
export OVS_WITNESS_VERSION
extension="extensions/ovs-witness/$version"
out="$repo/outputs/ovs-witness-ci"
mkdir -p "$out" test-results/ovs-witness
test "$(uname -m)" = "${EXPECTED_MACHINE:?}"
CGO_ENABLED=0 go build -trimpath -o "$out/decoder" ./tests/ovs_witness/decoder
go test -race -c -o "$out/preflight.test" ./tests/ovs_witness/preflight
go test -race -c -o "$out/multicast-execution.test" ./tests/daemon/field_execution
cp "$(go tool -n test2json)" "$out/test2json"
go test -json -count=1 ./internal/ovswitness > test-results/ovs-witness/go-codec.json
go test -race -json -count=1 ./internal/provider/ovsdb > test-results/ovs-witness/go-provider.json
go test ./internal/ovswitness -run '^$' -fuzz '^FuzzWitnessDecode$' -fuzztime=3s -parallel=2 > test-results/ovs-witness/go-fuzz.log
commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$extension/upstream.json")
curl --fail --location --retry 0 --max-time 90 \
  "https://api.github.com/repos/openvswitch/ovs/tarball/$commit" -o "$out/upstream.tar.gz"
python3 - "$out/upstream.tar.gz" "$extension/upstream.json" <<'PY'
import hashlib, json, sys
from pathlib import Path
expected = json.loads(Path(sys.argv[2]).read_text())['archive_sha256']
assert hashlib.sha256(Path(sys.argv[1]).read_bytes()).hexdigest() == expected, 'Untrusted archive'
PY
mkdir "$out/source"
tar -xzf "$out/upstream.tar.gz" --strip-components=1 -C "$out/source"
python3 "$extension/apply.py" "$out/source" "$out/upstream.tar.gz"
python3 tests/ovs_witness/check_registry.py "$out/source"
cd "$out/source"
./boot.sh > "$repo/test-results/ovs-witness/bootstrap.log" 2>&1
for mode in enabled sanitized default; do
  mkdir "$out/$mode"
  cd "$out/$mode"
  flags=()
  if [ "$mode" != default ]; then flags+=(--enable-ovs-webui-witness); fi
  cflags='-O1 -g -Werror'
  ldflags=''
  if [ "$mode" = sanitized ]; then
    cflags+=' -fsanitize=address,undefined -fno-omit-frame-pointer'
    ldflags='-fsanitize=address,undefined'
  fi
  "$out/source/configure" --enable-ssl --disable-libcapng --disable-shared --enable-static \
    --prefix="$out/install-$mode" "${flags[@]}" \
    CFLAGS="$cflags" LDFLAGS="$ldflags" LIBS='-lm' > "$repo/test-results/ovs-witness/configure-$mode.log" 2>&1
  make -j4 > "$repo/test-results/ovs-witness/build-$mode.log" 2>&1
  if [ "$mode" != default ]; then
    unitflags=()
    if [ "$version" = 4.0.0 ]; then unitflags+=(-DOVS_WEBUI_WITNESS_RESET_API); fi
    ./libtool --mode=link gcc $cflags $ldflags "${unitflags[@]}" -I. -I./include -I./lib \
      -I"$out/source" -I"$out/source/lib" -I"$out/source/include" \
      "$repo/tests/ovs_witness/unit.c" -Wl,--start-group \
      ofproto/libofproto.la ovsdb/libovsdb.la lib/libsflow.la lib/libopenvswitch.la \
      -Wl,--end-group -lssl -lcrypto -lpthread -lm -lrt \
      -o witness-unit > "$repo/test-results/ovs-witness/unit-build-$mode.log" 2>&1
    ASAN_OPTIONS=detect_leaks=1:abort_on_error=1 UBSAN_OPTIONS=halt_on_error=1 \
      ./witness-unit > "$repo/test-results/ovs-witness/unit-$mode.log" 2>&1
  fi
  if [ "$mode" != sanitized ]; then
    for protocol in stp rstp; do
      make -j4 check TESTSUITEFLAGS="-k $protocol" \
        > "$repo/test-results/ovs-witness/upstream-$mode-$protocol.log" 2>&1
      cp tests/testsuite.log "$repo/test-results/ovs-witness/upstream-$mode-$protocol-suite.log"
    done
  fi
  cd "$repo"
  sudo -- env OVS_WITNESS_VERSION="$version" ASAN_OPTIONS=detect_leaks=0:abort_on_error=1 UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1 \
    python3 tests/ovs_witness/prototype.py --build "$out/$mode" \
    --source "$out/source" --mode "$mode" \
    --report "test-results/ovs-witness/native-$mode.json"
done
python3 tests/ovs_witness/manifest.py "$out"
if [ "$version" = 3.3.9 ]; then
  # Full production HTTPS/IPC admission and Svelte execution, with a separate
  # explicitly configured optional source-access profile in the disposable VM.
  # All default and source-denial gates remain unchanged.
  ui_status=0
  sudo -- env PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:?}" python3 tests/daemon/inventory.py --bin-dir outputs/runtime --frontend-browser \
    --frontend-spanning-execution --witness-bin-dir "$out/enabled" \
    --browser-node "$(command -v "${WITNESS_BROWSER_NODE:?}")" || ui_status=$?
  for proof in test-results/go-frontend.json test-results/go-frontend-journal.log; do
    if [ -f "$proof" ]; then cp "$proof" test-results/ovs-witness/; fi
  done
  test "$ui_status" = 0
fi
