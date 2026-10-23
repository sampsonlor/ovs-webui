# Native multicast snooping — implementation and acceptance

Status: implemented on the feature branch, awaiting native acceptance; not deployed.
Related to #43 / SW-11.
The accepted STP and native Port isolation baselines remain separate gates.

## Scope and semantics

The closed operation `bridge.multicast.configure` edits the existing native
Bridge snooping flag and three basic native `other_config` keys:

- `mcast-snooping-aging-time`: integer string, 15–3600 seconds.
- `mcast-snooping-table-size`: integer string, 10–1000000 groups.
- `mcast-snooping-disable-flood-unregistered`: exact `true` or `false`.

Explicit null removes a key; omitted keys preserve their captured native
values. The native compiler merges from the complete private persistent map;
the filtered public projection is never a native rewrite source. Unset values
remain unset in Diff and compensation, rather than being
replaced with guessed native defaults. Other map entries are preserved.
Configured snooping is neither proof of group membership nor measured delivery.
Advanced multicast policy and forced flooding combinations remain read-only.
The legacy proposal `multicast.configure` is not an executable compatibility
alias and cannot bypass the new typed authority or validator.

## Authority, graph and recovery

The reserved `ovs.bridge.multicast.write` capability and immutable root process
`--local-multicast-bridges` grant are independent of topology, VLAN, STP and Port
isolation grants. Builtin role templates do not inherit it. Offline root
`--grant-multicast-role` / `--revoke-multicast-role` actions are audited and
idempotent; old sessions, grants and token ceilings do not acquire new rights.
Mode depth never changes permissions. Tablet/mobile do not stage this change.

Capture the full original flag/parameter group and connected persistent graph.
Edits preserve the sealed original; rebase cannot replace it. Root grant ABA
changes the authority revision. Admission requires a fresh confirmed generation,
known supported schema fields, a system Bridge with intact local Port and at
most 32 Ports, each with one supported system/internal Interface. Unknown,
externally controlled or shared identities remain unavailable. STP/RSTP,
protected Ports, Bonds/LACP, Mirror references, QoS, exporters, controllers,
offload, QinQ and advanced multicast flooding/policy combinations are not
qualified. This slice does not claim they were implemented by denying them.

Use shared Candidate → Diff / Validation → high-risk Safe Apply → Events/Audit.
Reserve graph members durably. A single native transaction guards the complete
persistent images in all four graph tables and settled counters, then writes
only the closed flag/keys, private commit marker and next_cfg, with a durable
commit. Fresh exact marker/after-image/counter/Interface-health evidence is
required for Applied. A lost reply invents no target and authorizes no replay.
Guarded compensation restores exactly the captured values and leaves a later
external change untouched as RollbackConflict. Terminal recovery is read-only.

## Independent native group / Port observation

Bridge detail adds `multicast_groups`; listing a page does not trigger one
native command per row. The optional observer is configured only through four
root process flags: `--multicast-group-witness-socket`,
`--multicast-group-witness-executable`, `--multicast-group-witness-manifest`,
and `--multicast-group-witness-manifest-sha256`. These flags grant no writes.
The existing qualified optional OVS UUID/name witness build is required;
ordinary distro OVS remains explicit Unavailable for this independent source.

Within one two-second attempt, pin the actual control peer, executable/build,
DB socket pair/file/root/schema and Bridge UUID/name/incarnation. The build's
compiled schema must match the actual DB schema. Read one fixed `mdb/show`
command between two matching installed/IDL/authority witnesses; no shell,
subprocess, client-supplied verb, retry or weaker fallback exists in production.
All source probes have the same bounded attempt lifetime as STP source checks.
There is one concurrent native group read, a 64 KiB native text budget, 256
entries, strict IGMP/MLD/router decoding, canonical membership duplicate checks,
and independent bounded JSON transport validation. Unrecognized or truncated
output rejects the whole observation.

Recheck the current confirmed persistent graph and ofport associations after
collection, then map every native group to a unique Bridge → Port → Interface
association. Do not attach a same-name replacement. Source failure, stale or
changed association, expired sample, disabled snooping, withheld configuration,
missing native source and a successfully observed empty table are distinct.
Unavailable/withheld uses null items; only an observed empty table uses `[]`.
No previous group table is silently reused as fresh. Bridge configuration
freshness and native group freshness are shown independently in both modes.

## Required acceptance (not yet passed)

Local tests cover closed/unset values, independent authority and credential
ceilings, full-graph dependency conflicts, native plan preservation, strict
bounded group decoding and stale/changed/withheld association rejection. Linux
cross-compilation is not actual native acceptance.

The dual-architecture distro OVS matrix uses three database schemas and must
pass native IPv4/IPv6 listener/nonmember packet forwarding before enable, after
Safe Apply and after exact compensation. Reopen the real manager database with
new auth/inventory/executor components while provisional, then require timeout
compensation from the durable checkpoint exactly once. Also require lost
replies, external conflict and no replay. Separately, the three actual pinned OVS builds (3.3.9, 3.7.1 and
4.0.0) must pass the same writer/packet checks in default, enabled and
ASan/UBSan variants on both architectures. Seven bound group-source cases are
additionally required in enabled and ASan/UBSan variants. Schema fixtures
are not described as distinct running binaries. Existing native witness,
upstream, authority, deadline, recovery and all regression gates remain. Each
actual-build writer runs in its own disposable network namespace so its daemon
cannot contend with the independent witness daemon for a kernel datapath.

Two new formal real-service browser cases cover read permissions, empty/stale
views and restoration, Standard/Expert, light/dark, desktop/tablet/mobile,
closed configuration staging, shared validation, confirmation, exact recovery
and explicit external-conflict restaging. A separate actual-build browser case
must show known empty and populated IGMP/Port observations, mode/device
boundaries and a deliberately mismatched build returning Unavailable rather
than an empty table. Screenshots require actual review after exact source CI.

Full source CI, official artifact digest verification, screenshot review,
merge, independent main qualification and an annotated accepted baseline are
all pending. #43 remains open; Tunnel, Mirror, QoS/Queue, exporters and
Profile/Drift are still required. #71 remains deferred. No production deployment.

## Native references

- [OVS 3.3.9 schema documentation](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/vswitch.xml)
- [Native mdb/show formatting](https://github.com/openvswitch/ovs/blob/v3.3.9/ofproto/ofproto-dpif.c)
- [Native membership protocol names](https://github.com/openvswitch/ovs/blob/v3.3.9/lib/mcast-snooping.c)
