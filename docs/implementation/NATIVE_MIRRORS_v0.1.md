# Native Mirror sessions v0.1

This implements P1-SW-07 / SW-13 as a first-class native Mirror object. Acceptance
requires the exact source CI, actual screenshots and an independent main run.
This document alone does not establish acceptance or complete #43 / Phase 1.

Current disposition (2026-10-28): source `ce31eeb`, CI run `40628786740`, is
rejected. Both native architectures and the actual pinned OVS builds still
deliver old Mirror copies after deletion, including retained snaplen/RSPAN
effects, despite empty Bridge.mirrors and settled configuration counters.
The pinned unregister implementation does not request cached-flow revalidation.
The [minimal correction and build-admission proposal](../../extensions/ovs-mirror-delete/README.md)
was approved by the user on 2026-10-28. Optional correction and independent
build admission are implemented; full native qualification remains pending. Formal UI
steps passed, but no UI result overrides this native failure. PR #108 stays
Draft and no Mirror baseline or production deployment is accepted.

## Scope and shared workflow

`/switching/mirrors` provides native session lists, immutable detail links and
desktop preparation of creation, configuration and deletion. Select source
traffic entering Ports, destination traffic leaving Ports, or all Bridge Ports;
an independent VLAN filter also applies to select-all. Zero selects untagged
traffic. Configure exactly one reserved output Port or RSPAN VLAN, with optional
snap length. Empty native optional sets remain unset. Session names are immutable.

The closed `mirror.session.create/configure/delete` intents use the shared
Candidate → native Diff/Validation → Safe Apply → confirmation/recovery →
Job/Event/Audit path. There is no live save or raw OVSDB input. Packet capture
remains a separate Diagnostics workflow. Native copy counters are observations;
configuration, counters and local Applied proof do not establish receiver delivery.

Standard/Expert share permissions. Tablet and mobile support review and existing
transactions; new changes require desktop. Empty results, permission denial,
unknown counters/configuration, stale observations, unsupported native filters,
retired identities and incomplete Port choices remain explicit. The creation
selector reads `inventory/topology?domain=mirror`: only root-provisioned creation
Bridges and their observed Port/Interface/Mirror graph. Server validation still
reads and guards the full native dependency graph. A topology view over 64 nodes
or the existing authenticated 48 KiB response ceiling closes creation; detail
choices are bounded to 32 Ports.
The existing 32-object transaction and IPC/plan/snapshot budgets still apply.

## Independent authority and qualified native graph

`ovs.mirror.write` is a reserved account capability provisioned by the offline
root actions `--grant-mirror-role` / `--revoke-mirror-role`. Existing credential
ceilings and current role revocation remain enforced. Core topology or Tunnel
authority does not confer Mirror authority. New writes also require the separate
verified runtime build described in the correction/admission document.

`--local-mirror-objects` grants exact current management IDs (maximum 256).
`--local-mirror-create-targets` grants exact Bridge-management-ID:session-name
pairs (maximum 128). A policy change, including remove-and-restore, invalidates
an old review. Created and restored sessions require explicit grants of their
fresh IDs before another change; a name does not transfer ownership.

Qualified Bridges use native system/NORMAL switching and known complete
Bridge → Port → Interface relationships. A selected Port must belong to the
same Bridge and have one eligible Interface. Existing native output Port
selection conflicts and local internal output selection are rejected. The UI
explains that an output Port is reserved and discards incoming frames, and that
RSPAN can copy to multiple Ports with loop, performance and connectivity impact.

Controller control, DPDK/offload, Bond/LACP, protected Ports, STP/RSTP,
multicast snooping, QoS/exporters and other unqualified combinations remain
read-only for this writer. OVS 3.3.9 has no Mirror.filter; 3.7.1/4.0.0 optional
empty filters retain their raw value. Nonempty expressions remain private and
read-only. Schema constraints, cardinalities, reference strengths and persistent
column shapes are verified rather than inferred from a version string.

## Native transactions and durable recovery

The manager captures the full persistent Mirror image separately from public
configuration summaries and observed statistics. The executor derives only the
managed native selector/output/snaplen fields and preserves foreign metadata.
Creation attaches a fresh Mirror to Bridge.mirrors; deletion removes the strong
reference and permits native garbage collection.

Every Mirror mutation and Applied proof fence the complete persistent
Open_vSwitch/Bridge/Port/Interface/Mirror graph in the same native transaction,
including unchanged sessions on other Bridges. Incoming Port/Mirror references
are checked explicitly, separately from core topology links. Unknown map-valued
incoming references block this writer. Root counters, commit marker, durable
commit, host interface identity and native allocation/error checks remain active.

Rollback uses the sealed original, requires current == our-after and refuses
external graph changes. Deletion recovery remaps Bridge.mirrors to a reserved
fresh native UUID and management ID, preserving native fields and metadata.
Manager database reopen and the existing recovery deadline retain this plan.
Lost replies leave the target unknown and never authorize a replay or invented
Applied claim. Admitted recovery keeps its sealed authority independently of
new write admission.

## Qualification evidence

Five native kernel cases cover source direction, destination direction,
select-all with an independent VLAN filter, snap length and RSPAN VLAN. Each
exercises packet observations, exact edit compensation, deletion, manager state
reopen/deadline recovery, fresh identities, late unrelated dependencies, lost
replies, no replay and external conflicts. Packet sockets and a native
revalidator-cycle barrier run only in disposable test namespaces; they add no
production packet-delivery guarantee.

Separate amd64/arm64 jobs retain unfixed distro counterexamples with three
schema fixtures, then run the full matrix with the qualified fixed 3.3.9 runtime
and those exact schemas. Actual pinned OVS 3.3.9/3.7.1/4.0.0 enabled/ASan-UBSan
builds run the full matrix against their own schemas; defaults reproduce the
unfixed residual and deny manager writes. These evidence kinds remain separate.
Two formal real-service browser cases add 21 screenshots per architecture and
retain the existing tests, deadlines and retries. All prior native, auth,
recovery and browser gates remain required. #71 stays deferred; no deployment.

Native semantics: [OVS 3.3.9 Mirror schema documentation](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/vswitch.xml)
and [native selector implementation](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/bridge.c).
