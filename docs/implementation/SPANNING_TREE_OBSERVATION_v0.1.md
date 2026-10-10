# STP / RSTP observation · M1

Date: 2026-10-10. Parent engineering issue: [#43](https://github.com/sampsonlor/ovs-webui/issues/43). Functional owner: [#23](https://github.com/sampsonlor/ovs-webui/issues/23), SW-10 / P1-SW-04. This batch establishes the formal read surface. Basic Manage, protocol validation at dispatch, Applied proof and compensation remain separate M2 work. Neither parent is complete after M1.

## Public and native boundaries

The Svelte page `/switching/spanning-tree` uses `GET /api/v1/inventory/spanning-tree`; detail `/switching/spanning-tree/{bridge_id}` uses `GET /api/v1/bridges/{bridge_id}/spanning-tree`. The identity is the existing immutable Bridge management ID. There is no new native STP object. Original generic `/spanning-tree` contract routes and the pending `spanning_tree.configure` intent retain their existing service gate. API 1.25.0 adds typed observation routes without replacing released response references or adding required fields to existing resources.

Both reads require `state.read` at manager authorization and `inventory.read` at inventory admission. Configuration, Interface-type participation and ownership additionally require `configuration.read`. Observation does not grant `workspace.write` or any OVS write capability. All returned `editable` values are false and `allowed_operations` is empty. There is no product OVSDB mutation, direct save, timer, automatic reconciliation or command runner in M1.

The OVSDB adapter discovers exact boolean and string-map columns from the schema. It monitors Bridge enablement, approved spanning-tree `other_config` keys and Bridge/Port `status` and `rstp_status`. Public map keys and values are bounded. Complete private persistent configuration remains separate from public sanitized fields. Invalid or oversized presence becomes Unknown rather than an invented unset/default. Runtime columns remain excluded from configuration checkpoints and configuration revisions. Runtime changes update the inventory snapshot, not the spanning-tree desired-configuration revision.

## Meaning of observations

| Evidence | Interpretation |
| --- | --- |
| Bridge `stp_enable` / `rstp_enable` | Configured intent, independently sourced from OVSDB configuration. Both true is `invalid-both-enabled`; M1 never chooses a protocol. |
| Explicit parameters | Preserve the native string, including a value that a future validator might reject. This read does not certify validity or application. |
| Absent parameter in a known map | `unset`; the page says native default, without materializing an unobserved number. Missing column/map, unsupported shape and withheld configuration are distinct. |
| `status` / `rstp_status` | Daemon-reported observation through OVSDB, with independent source and timestamp. Missing or unrecognized state/role is Unknown. No claim of convergence or packet forwarding. |
| Bond / internal Port | Native documented exclusion; no type inference from names. |
| Single verified system/default-system or synthetic dummy Port | Show the native Port enable override or its default. This assessment applies only when the Bridge's corresponding protocol is enabled. Dummy is an isolated test device, not evidence for another production provider. |
| Bridge with mirrors | Output-Port relation is not monitored in M1; participation is Unknown for otherwise eligible Ports. Never guess which Port is a mirror output. |
| Other or unknown Interface type | Participation unverified; runtime observations may still be shown. No tunnel/DPDK/offload activation. |
| Stale snapshot | Keep last observed values with stale provenance, while current participation becomes Unknown. |
| External ownership | Root/Bridge/Port/member external markers and Bridge controllers remain evidence of external control, never write grants. Unknown ownership never becomes local authority. |

Bridge and Port detail reuse the same projection helpers as SW-10. A selected Bridge's Ports come from one immutable snapshot and are naturally ordered. Detail is bounded by 128 Ports and 52 KiB with explicit `ports_truncated`; the page links to the remaining Bridge/Port/Interface inventory. List pagination retains existing principal/permission/generation/snapshot/filter/limit cursor binding. A retired or recreated same-name Bridge cannot satisfy an old detail link.

## Remaining #43 domain gates

Shared Port detail carries a compact spanning-tree summary (enable overrides, state/role, participation and ownership). Advanced parameters stay in the dedicated spanning-tree detail. Ordinary Bridge/Port/Bond lists omit this duplicate projection and retain their native fields and bounded pagination; callers locate a specific named resource with the existing filter rather than assuming it appears on the first page. Common detail fields use the same projection and native provenance in both views.

These mappings identify the next evidence requirements; they do not grant new capabilities or reduce approved scope.

| Domain / owner | Authority and validator boundary | Applied / recovery gate | Current delivery |
| --- | --- | --- | --- |
| STP/RSTP, #23 / #43 | Separate Bridge field authority; STP/RSTP mutual exclusion, native priorities/timer relationships and nonparticipating Port/type/mirror graph; controller and external ownership must be checked at execution | Guarded enablement/parameter image, actual daemon acknowledgment and compensation under changed dependencies, lost reply and watchdog restart | M1 observation; all Basic Manage writes pending M2 |
| Multicast, #43 | Native Bridge/Port fields, schema and independent authority; native snooping limitations | Native application and guarded prior-field restoration | Pending |
| Tunnel, #43 | Approved native Interface types/options, peer/underlay dependencies and provider authority | Native object/runtime and endpoint recovery; unsupported combinations remain Observe/Unavailable | Pending |
| Mirror, #43 | Native Mirror object/Bridge references, selection/output restrictions and VLAN interaction | Atomic native references and compensation without severing foreign ownership | Pending |
| QoS / Queue, #43 | Native QoS/Queue schemas, queue mapping and independent scheduler ownership; existing ingress policing does not prove this domain | Provider-qualified scheduler application and exact guarded restoration | Pending |
| sFlow / NetFlow / IPFIX, #26 / #43 | Three independent native objects/references, exporter fields and authority | Native reference/application and Safe Apply compensation; exporter configuration is not collector delivery proof | Pending |
| Native Isolation, #43 | Only approved OVS native semantics with VLAN/Bond/SLB/forwarding constraints | Native state and safe compensation; do not emulate missing behavior with scripts/flows | Pending |
| Profile / Drift, #43 | `managed_fields`, profile revision, one binding per Port plus explicit override; In Sync/Drifted/Outdated/Unsupported/Unknown | Explicit Accept Current / Reapply / Detach through shared authority and change evidence; no automatic reconciliation | Pending |

All later writable slices must use Candidate → Diff/Validation → Apply or Safe Apply → shared Job/Event/Audit, with separate capability/authority/validator/recovery evidence. Existing frozen VLAN, Bond, topology, OpenFlow and DPDK/offload boundaries remain in force.

## Review and acceptance

M1's review disposition and evidence are in [the batch review](../reviews/SPANNING_TREE_OBSERVATION_v0.1.md). Required evidence includes real native reads on all three schema fixtures on both architectures, formal Go/OVS browser reads, normal/empty/unknown/withheld/conflict/external/stale/unavailable/retired states, Standard/Expert depth, keyboard access, light/dark and desktop/tablet/mobile observation. Synthetic unit projections do not replace native acceptance. This batch does not claim writable recovery acceptance, network-wide loop prevention or Phase 1 release acceptance.

Native semantic sources: [OVS database manual, Bridge spanning-tree and Port STP/RSTP sections](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html), alongside the pinned upstream schema fixtures 3.3.9, 3.7.1 and 4.0.0. RSTP priority uses multiples of 4096 and native timers have protocol constraints; observing a raw value is not validating or normalizing it.
