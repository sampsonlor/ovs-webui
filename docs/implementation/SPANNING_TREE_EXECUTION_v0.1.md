# STP / RSTP guarded execution

Status: implementation under qualification. This document does not close #23
or the wider #43 aggregate. Source and main acceptance evidence must be recorded
before a release claim. No production deployment has been performed.

## Authority and configuration

`bridge.spanning-tree.configure` is independent of the existing
`bridge.spanning-tree.prepare`. A prepare envelope stays unconditionally blocked
and cannot be promoted by restaging. The legacy `spanning_tree.configure` stays
unsupported. Execution requires configuration read, workspace/apply permissions,
an explicitly assigned `ovs.bridge.spanning-tree.write` capability and both
independent root Bridge identity grants.

Builtin role templates do not acquire the new capability. An offline root
operator can provision or revoke an existing role with
`--grant-spanning-tree-execution-role <role-management-id>` or
`--revoke-spanning-tree-execution-role <role-management-id>`. These commands exit
after the audited policy change. Old credentials cannot acquire a larger
ceiling; a new login is required after provisioning. There is no new HTTP or IPC
provisioning method and no database editing requirement.

The manager defaults to execution disabled. An opt-in deployment needs all of:

- `--local-spanning-tree-bridges` with the reviewed Bridge management IDs;
- `--local-spanning-tree-execution-bridges` with independent execution grants;
- `--spanning-tree-witness-socket` with the explicit local daemon control socket;
- `--spanning-tree-witness-executable` with the exact root-owned executable;
- `--spanning-tree-witness-manifest` and
  `--spanning-tree-witness-manifest-sha256` with the reviewed build manifest;
- the existing independently configured Safe Apply management endpoint and
  interface.

Only the three exact compiled build profiles (OVS 3.3.9, 3.7.1, 4.0.0) and the
qualified amd64/arm64 variants are eligible. Version text, a schema label or a
manifest alone cannot enable execution. The readonly OVS extension remains
unchanged: it exposes no write command or lock acquisition method. Deployment
still needs a concrete host/version, maintenance plan, backup and explicit
approval; these flags are not installed or activated by this implementation.

## Execution and recovery

Candidate captures the two flags, seven raw basic keys (including unset values),
immutable Bridge binding and private persistent graph dependency. Inactive keys
remain preserved and validated. Configuration validation checks mutual exclusion,
the native parameter constraints and both native setter directions. A valid
final RSTP timer pair can still be blocked when native setter order cannot install
or restore it in one pass. No intermediate timer or protocol steps are invented.

The journal durably reserves the root graph and all admitted Bridge/Port/Interface
identities before dispatch. Repeated live admission reloads the credential ceiling,
current policy, frozen Validation/workspace and lease around witness I/O. The
one-shot `beforeSend` callback separately persists dispatch intent immediately
before mutation bytes. A prepare intent cannot reach this compiler.

The fixed native transaction atomically waits for complete persistent images of
Open_vSwitch, Bridge, Port and Interface, including the settled counters and all
references. It changes only the target Bridge's two flags, seven basic map keys,
private commit marker and the root configuration counter. Unrelated map entries
are preserved. There is no raw transaction input, shell command, flow change,
generic RPC or blind write retry.

Commit and Applied remain separate. Exact target counters, marker/after-image,
fresh atomic database fences and two matching installed witnesses are required
for Applied. Native process/build, root/Bridge UUID, runtime instance, binding and
authority epochs must remain bound. Both inactive engines must be absent for a
disabled result; an active result requires exactly its intended engine and the
actual expected local timers. This proves local installation, not network
convergence, packet continuity or a management path.

Witness lock observations are not a server-instant lock guarantee. A lock loss
or reconnect after the preflight can withhold Applied; it is never translated
into success from configuration or `cur_cfg` alone. Lost replies retain unknown
target/Applied outcomes and never cause replay. Reconciliation only observes.

Compensation comes exclusively from the durable original. The current marker and
complete after-image must still match before one guarded transaction restores
the original raw/unset values and flags. External changes are preserved as a
rollback conflict. Root/provider/build/installed-state uncertainty retains the
graph protections and requires recovery. Manager restart cannot resume an
uncommitted dispatch; a fresh compensation can use its durable checkpoint and
freshly rechecked root/native evidence. Loss of the initiating permission or
management reachability starts compensation and does not prohibit that frozen
recovery operation. Confirmation still requires actual Applied and independent
management reachability.

## Qualification

The isolated enabled and ASan/UBSan native fixtures retain all 21 readonly
composition cases and add 14 execution cases: all nine protocol endpoint
combinations except the disabled no-op (eight forward/compensation pairs), role
revocation, root revocation, field conflict, denied dispatch admission, RSTP
creation timer order and changed-after-image compensation refusal. The same
tests run on all three OVS versions and both architectures. They also check
lost-reply target uncertainty, preservation of unrelated map values, raw leading
zeros and unset values.

Manager repository tests separately cover the dedicated capability and old
credential ceiling, sealed operation, durable member reservations, confirmation
authority, management-loss compensation and no compensation replay. Existing
Safe Apply outage/restart/unknown-outcome matrices remain required. The existing
formal readonly/draft browser cases remain required; new execution UI still needs
its own browser acceptance evidence before #23 is closed.

The wider #43 also includes Multicast, Tunnel, Mirror, QoS/Queue, exporters,
Isolation and Profile/Drift. This STP implementation does not complete those
domains or change their accepted Phase 1 scope. #71 remains deferred and is not
altered by this work.
