# STP/RSTP M2c1 · native runtime reads and transition analysis

This batch completes the private read/analysis prerequisite for M2c and repairs the existing draft disclosure state. It does not enable configuration dispatch, change the public API, or add UI controls. `bridge.spanning-tree.prepare` still always emits `SPANNING_TREE_EXECUTION_PENDING`; preparation authority is not execution authority.

## Fixed-purpose native reader

`ovsdb.ReadSpanningTreeRuntime` directly connects to one explicitly supplied local Unix control socket. It sends exactly one `stp/show` or `rstp/show` request with exactly one validated basic Bridge name. There is no shell/appctl subprocess, arbitrary RPC method, socket discovery, automatic retry, write verb or echo handler. The caller supplies the expected peer UID; an optional prior daemon incarnation must match before any request is sent. The incarnation combines Linux boot ID, SO_PEERCRED PID and process start ticks, and is rechecked after the reply. No root command-line option or public route is added in this batch.

One two-second context bounds connect, write and read; cancellation closes the socket. The complete wire read has a 256 KiB budget including leading whitespace, strict JSON checks and independent text/line/field budgets. The shared JSON validator limits any decoded string to 64 KiB; the standalone text parser additionally caps input at 128 KiB, 1,024 lines and 1,024 bytes per line. Missing/duplicate/foreign fields, wrong reply ID, errors, unsupported output, missing protocol, missing Bridge and malformed units return an empty sample. They never imply a disabled protocol or replace a native observation with configured defaults or a cached value.

The result is private, name-scoped evidence carrying daemon incarnation and sample time. It does **not** prove that the process is the intended ovs-vswitchd instance for the manager's OVSDB, that the name still denotes the sealed immutable Bridge, that both protocols are exclusive, or that an execution was Applied. The next integration must bind the control daemon and database instance, immutable object identity, guarded configuration, graph, freshness and current authority around the sample. Root trust or a matching name alone is insufficient.

## Local parameters versus operational timers

The upstream text labels are not a safe source mapping by themselves:

| Protocol | Local priority and system ID | Local installed timers | Operational timers |
| --- | --- | --- | --- |
| STP | `Bridge ID` section | **`Root ID` section** | `Bridge ID` section; may be learned from the elected root |
| RSTP | `Bridge ID` section | `Bridge ID` section | Not inferred by this reader |

For STP, upstream prints the root identity together with local `bridge_*` timers in the first section, and the local identity with current operational timers in the second. The reader deliberately assembles only the local identity and local timer fields, retaining STP operational timers separately. It never attaches the root priority or root MAC to the local timers. A real two-Bridge patch-link fixture verifies a non-root Bridge with local 6/4-second timers while its operational timers are learned as 20/15 seconds. This fixture does not qualify patch graphs for production writes.

All timer values carry explicit seconds. Native numbers are preserved without clamping or rounding. Raw configured strings and unset values remain separate inputs. The earlier M2a nine-case fixtures used isolated root Bridges, where local and operational timers agreed; their observations remain valid within that scope.

## Bounded, bidirectional transition analysis

`spanningtree.AssessTransition` is a pure analysis function. Both before and after images must pass the existing basic validator, including inactive parameters, STP explicit-hello caveats and RSTP priority multiples. It accepts only an already active, unchanged protocol and requires the original local numeric parameters to match the independently read local sample. Native defaults are used only for comparison; the supplied raw maps are never changed or materialized.

For RSTP the native implementation sets max age before forward delay, and each setter checks against the currently installed peer. A valid desired pair does not prove either direction can install:

- Forward requires `after.max_age <= 2 * (before.forward_delay - 1)`.
- Rollback requires `before.max_age <= 2 * (after.forward_delay - 1)`.

Both are required for a `compatible` result. The 6/4 to 40/30 transition is rejected in the forward direction; its inverse is rejected because rollback would have that unsafe order. Native tests also retain the actual first-transition result (age 6, delay 30), reject treating the configured 40/30 pair as an installed original, and verify an overlapping 20/15 to 22/16 pair in both directions. A fixture-only subsequent priority reconfiguration demonstrates the retained-timer behavior; it is not a production retry or generated intermediate plan.

No automatic disable/re-enable, synthetic intermediate, normalization or implicit protocol selection is produced. Enable, disable, protocol switching, unsafe single-step pairs and recovery from partial multi-step state remain blocked or outside this analysis. `compatible` is a basic parameter/setter-order result, **not** an execution grant, a plan, an Applied result or a rollback authorization.

## Draft disclosure regression found during acceptance

The first CI (39393526797, source `8630f32`) failed an existing amd64 post-screenshot `open` assertion in the draft form; its exact dark screenshot and failure log are retained. An independent real-Svelte-component reproduction established that eligibility or desktop branch removal/recreation loses an unbound native details open state while preserving the entered fields. That is a confirmed state-management defect; the initial screenshot failure alone does not establish that branch removal was its only trigger.

The editor now binds disclosure state to component state and resets it when immutable Bridge/generation changes. Same-object eligibility restoration or a return to desktop retains the expanded draft and typed values. Current unavailable/tablet/mobile branches still remove the form and staging action; preserving local state never grants authority. The formal real-OVS browser case now withdraws preparation eligibility through an advanced native Bridge key, verifies the form/action disappear, restores the exact original native map, and requires expansion and values to survive. Expert/theme/screenshots and responsive return now assert expansion without reopening the form in the helper. No existing assertions, retries, authentication rules, timeouts or recovery gates were weakened.

## Acceptance and remaining M2c work

Seven new top-level Go regressions cover source mapping, malformed output, forward/rollback admission, fixed methods, malformed RPC, UID/incarnation checks and deadlines/cancellation. An explicit native CI group runs 13 cases on amd64 and arm64 against each of the 3.3.9, 3.7.1 and 4.0.0 schema fixtures, retaining the actual installed OVS binary version separately through the existing inventory reports. The existing draft-only admission/auth tests, browser suite, native writes/recovery matrices and diagnostic redaction remain mandatory. The state fix has no new product controls or intended layout change; retained desktop/depth/theme/responsive screenshots still require review.

Remaining M2c gates are dedicated execution capability/root authority, same-daemon/database/immutable-Bridge qualification, strict sealed execution DTO and graph reservations, guarded native writes preserving unrelated keys/unset originals, installed/exclusive-protocol proof at dispatch and Applied, High-risk Safe Apply, guarded compensation, and crash/restart/lost-reply/OutcomeUnknown/provider-degradation/conflict acceptance. #23/#43 and Basic Manage remain open; #71 remains deferred.

## Primary source basis

- [OVS 3.3.9 STP implementation](https://github.com/openvswitch/ovs/blob/v3.3.9/lib/stp.c): `stp_print_details`, local requested/installed versus operational timers.
- [OVS 3.3.0 RSTP implementation](https://github.com/openvswitch/ovs/blob/v3.3.0/lib/rstp.c): `rstp_print_details`, timer setters and current-peer constraints.
- [OVS 3.3.9 ofproto implementation](https://github.com/openvswitch/ovs/blob/v3.3.9/ofproto/ofproto-dpif.c): `set_rstp`, max-age-before-forward-delay ordering.
- [OVS 3.3.9 Unix control protocol](https://github.com/openvswitch/ovs/blob/v3.3.9/lib/unixctl.c): one named command, string arguments and JSON-RPC result/error handling.

Source references and real daemon acceptance qualify this narrow implementation; schema/version labels alone do not qualify a future write path.
