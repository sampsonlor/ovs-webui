# OVS 3.7.1 optional read-only witness prototype

Experimental source extension for `7921d9c6924b8934ea1de9481891ac1172649280` only. It has no production caller, installation command, execution operation or write permission. The existing `bridge.spanning-tree.prepare` blocker remains unconditional.

`witness.patch` adds opt-in build configuration, IDL event bookkeeping, per-process/ofproto identity, configuration lifecycle hooks and local STP/RSTP snapshot APIs. `overlay/` contains the handler and fixed input registry. Apply only to a disposable unmodified source tree:

```sh
python3 extensions/ovs-witness/3.7.1/apply.py /disposable/ovs-source /downloaded/upstream.tar.gz
```

The archive digest and every modified upstream input are checked against `upstream.json`. The default build has no new command. `--enable-ovs-webui-witness` requires OpenSSL and registers only `ovs-webui/stp-witness-v1 bridge-uuid nonce`. No new socket or network listener is created. OVS upstream code retains its Apache-2.0 license; added overlay files use the same SPDX license.

The isolated CI entry is `bash tests/ovs_witness/build.sh`, on native Linux amd64/arm64 with Go from `go.mod` and the prerequisites listed in `.github/workflows/ci.yml`. It creates separate default/enabled/ASan+UBSan builds and disposable kernel network namespaces. It does not install a package or operate on an existing OVS service. Do not point the test fixture at a production database.

Read [the prototype contract and limits](../../../docs/implementation/SPANNING_TREE_WITNESS_PROTOTYPE_v0.1.md) and [the accepted evaluation plan](../../../docs/implementation/SPANNING_TREE_RUNTIME_WITNESS_PLAN_v0.1.md). A known observation is neither execution authority nor an atomic guarantee of server lock ownership. Source/build metadata and complete native results are retained in the per-architecture `ovs-witness-*` CI artifacts. No release or deployment support is implied.
