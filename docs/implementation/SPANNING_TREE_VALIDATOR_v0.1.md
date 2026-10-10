# STP/RSTP native parameter validation · M2a

Status: implementation and review gate for the first M2 slice. Parent issues #23 and #43 remain Open / In Progress. The #71 investigation remains deferred in Pending.

## Delivered boundary

The M1 observation resource now includes optional `parameter_validation` (public API 1.26.0), computed from the same immutable Bridge snapshot. The independent `internal/spanningtree` package checks seven basic Bridge parameters and protocol mutual exclusion. It has no provider, Candidate or execution dependency.

The detail page shows human-readable findings in Standard and Expert modes. Expert additionally shows the rule identifier and scope. Permissions and rules are identical in both modes. Desktop, tablet and mobile may review the assessment; this batch introduces no configuration action on any viewport.

`valid` means the **basic parameter values satisfy this rule set**, including inactive protocol settings. It does not mean that advanced Bridge or Port settings are supported, that the graph is eligible, that an installed daemon value matches configuration, that the Bridge is locally controlled, or that a transition is safe. Existing Observe mode, empty allowed operations and the pending write gate remain enforced. The released, pending `spanning_tree.configure` intent is not admitted by Candidate.

## Parameter rules

| Native Bridge key | Accepted decimal value | Native default used only for rule evaluation |
| --- | --- | --- |
| `stp-priority` | 0–65535 | 32768 |
| `stp-hello-time` | 1–10 seconds; explicit 2–10 require runtime proof | 2 |
| `stp-max-age` | 6–40 seconds | 20 |
| `stp-forward-delay` | 4–30 seconds | 15 |
| `rstp-priority` | 0–61440, multiple of 4096 | 32768 |
| `rstp-max-age` | 6–40 seconds | 20 |
| `rstp-forward-delay` | 4–30 seconds | 15 |

- STP: `max_age >= 2 * (hello_time + 1)` and `max_age <= 2 * (forward_delay - 1)`.
- RSTP: `max_age <= 2 * (forward_delay - 1)`. Native hello time is fixed at 2 seconds; there is no Bridge `rstp-hello-time` key. The minimum max age of 6 already satisfies the native lower relation with hello time.
- Enabling both protocols is invalid. No automatic protocol selection or correction occurs.
- Decimal digits are an explicit product input boundary. Alternative native numeric representations, signs, whitespace, fractions and overflow require review; they are never silently converted. Leading decimal zeros retain their original observation.
- Defaults do not materialize in the native map or public configuration fields. An absent key remains `unset`; the validator does not fabricate a runtime default.
- Checks contain bounded codes and approved field names, never arbitrary native values or unrelated metadata. Advanced Bridge keys and Port parameters are outside this basic validator's scope.

## Availability and permissions

The assessment requires `configuration.read` in addition to the existing state/inventory permissions. Withheld inputs produce `withheld` with an empty check list, even if the hidden configuration is invalid. Stale observations produce `stale`, missing/invalid observed presence produces `unknown`, and unsupported native column shapes produce `unsupported`; none becomes a validity oracle. Independent runtime observations retain their M1 permissions and provenance.

Bridge detail and spanning-tree detail reuse this exact projection. Parameter assessment does not change the configuration revision, checkpoint classification, field authority or Safe Apply policy. Future unknown assessment states are displayed as unavailable.

## Native evidence and an execution gate that remains open

OVS stores several parameters as free-form strings. A successful OVSDB transaction therefore does not establish valid or installed timer values:

1. STP clamps hello time, then max age, then forward delay. Configured max age 5 produces installed max age 6; configured max age 20 / forward delay 4 produces installed delay 11. Both configurations are flagged for review.
2. Real OVS 3.3.9 also revealed an upstream STP hello-time unit caveat: `bridge.c` passes an explicit value directly to a millisecond setter, while the absent-key default is 2000 ms. Configured 10 seconds produces installed 1 second. Explicit values 2–10 are consequently marked `runtime-unverified`, even if their documented timer relations are valid or STP is inactive. Future schema/version labels cannot prove a correction; independent installed timer evidence remains required.
3. RSTP rounds priority down to a multiple of 4096. Configured 4097 remains visible as 4097 while the daemon uses 4096; the validator reports the mismatch risk.
4. RSTP may retain its previous timer when a requested value fails the relation. Configured max age 20 / forward delay 4 leaves the default installed delay 15.
5. Even a valid final RSTP pair does not prove a safe transition: the native implementation sets max age before forward delay and checks each against the **current installed** peer timer. Forward execution and compensation must both account for that order. Basic validation intentionally makes no transition or Applied claim.

Primary evidence was checked against upstream tags v3.3.0, v3.3.9 and v3.6.0:

- [Native database manual](https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html), Bridge STP/RSTP settings.
- [v3.3.9 Bridge configuration](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/bridge.c): explicit STP hello-time conversion; SHA-256 `5a4d09c02cd4622d2d9f18c9e788d7c886f0c36a5a9037d69d5988ffffb21ebb`. The same call is present in the inspected v3.6.0 source.
- [v3.3.0 STP implementation](https://github.com/openvswitch/ovs/blob/v3.3.0/lib/stp.c): `stp_update_bridge_timers`; SHA-256 `2353f3c8b082338331bdfec5f4eb6678076d8470eb71e9d8fedcce56dc097169`.
- [v3.3.0 RSTP implementation](https://github.com/openvswitch/ovs/blob/v3.3.0/lib/rstp.c): priority, hello-time, max-age and forward-delay setters; SHA-256 `0594f994f7f8facf53b7ca344946315ea3975b5e2b2b0196c9c303145a835c2f`.
- [v3.3.0 ofproto implementation](https://github.com/openvswitch/ovs/blob/v3.3.0/ofproto/ofproto-dpif.c): `set_rstp` setter order; SHA-256 `70188bd715c492c55112fd0beeb0ca32a6fa464cc0db325d60c771fdaef5c95b`.
- Corresponding [v3.6.0 STP](https://github.com/openvswitch/ovs/blob/v3.6.0/lib/stp.c), [RSTP](https://github.com/openvswitch/ovs/blob/v3.6.0/lib/rstp.c) and [ofproto](https://github.com/openvswitch/ovs/blob/v3.6.0/ofproto/ofproto-dpif.c) retain these rules. Version labels alone are not schema or runtime capability proof.

## Review evidence

- Go regressions cover native ranges and relational boundaries, malformed/overflowing values, raw/default preservation, inactive settings, simultaneous enablement, unknown input, shared resource consistency, external control and permission/stale/schema isolation. Explicit hello-time support stays runtime-unverified instead of being promoted by documented ranges. A Candidate regression keeps the pending operation closed.
- `tests/daemon/spanning_tree_parameters.py` runs nine cases on real isolated OVS, compares configured values and the read-only assessment with the daemon's own local Bridge priority/timers, and checks raw unset defaults, withheld assessment, retired identities and closed write gates. Shell tools occur only in disposable test setup and inspection.
- Existing CI invokes that acceptance for all three schema fixtures on amd64 and arm64. The report records the installed OVS binary version separately; three database schemas do **not** imply three runtime releases were tested.
- Formal browser acceptance includes normal parameter checks, invalid RSTP priority, inactive STP timer conflicts, permission withholding and stale assessments. Standard/Expert, desktop/tablet/mobile and dark/light evidence are retained with the existing M1 suite. Existing native execution, auth, transport, recovery and diagnostic gates remain required.

## Next M2 slice

Before opening Basic Manage, complete the following independently reviewable gates:

- Root-owned Bridge field authority and a dedicated capability, independent of topology/VLAN/Bond/Interface grants; native schema mutability is insufficient.
- Sealed before/after fields and immutable bindings, full graph/type/Mirror/controller/external authority validation, explicit unsupported combinations and strict payload semantics for the pending intent (including RSTP hello-time).
- Candidate stage/review, Diff, native validation and current policy revalidation without rebase silently replacing captured originals.
- Exact, guarded native updates preserving unrelated map keys and unset originals; graph reservations and current authority checked again at dispatch.
- Bounded, independently sourced installed protocol/priority/timer evidence from the same daemon and Bridge identity. `cur_cfg` or configured booleans alone cannot prove these values. Any daemon control probe must be fixed-purpose native transport, never arbitrary shell execution.
- High-risk Safe Apply confirmation, safe timer ordering in **both** directions, guarded compensation, lost replies, crash/restart, drift and OutcomeUnknown without write replay. No automatic disable/re-enable workaround for timer ordering.

Port parameters, advanced Bridge fields and all other #43 native domains retain their separate scope. M2a does not complete Basic Manage, #23, #43 or Phase 1.
