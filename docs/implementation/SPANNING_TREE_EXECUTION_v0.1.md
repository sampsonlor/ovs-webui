# STP / RSTP guarded execution

Status: guarded Basic Manage accepted. Final source
`8531af1fcecda1d5ab88dacf11272c27808700ee` passed complete
[CI 39886539800](https://github.com/sampsonlor/ovs-webui/actions/runs/39886539800).
Main `fb2513cb6164e1e60db3a9e7a7a60d61f2df9218` independently passed
[CI 39890159942](https://github.com/sampsonlor/ovs-webui/actions/runs/39890159942),
with the identical tree. Both runs passed all fourteen jobs and thirteen official
artifact digests were verified per run. Baseline:
`phase1-spanning-tree-execution-v0.1`; #23 is completed.
Release/main acceptance is recorded with [#23](https://github.com/sampsonlor/ovs-webui/issues/23).
The wider [#43](https://github.com/sampsonlor/ovs-webui/issues/43) remains open.
No production deployment has been performed; activation remains an explicit
host/build/security decision.

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
- `--spanning-tree-database-executable` and `--spanning-tree-database-sha256`
  pinning the reviewed standalone ovsdb-server build;
- the existing independently configured Safe Apply management endpoint and
  interface.

Only the three exact compiled build profiles (OVS 3.3.9, 3.7.1, 4.0.0) and the
qualified amd64/arm64 variants are eligible. Version text, a schema label or a
manifest alone cannot enable execution. The readonly OVS extension remains
unchanged: it exposes no write command or lock acquisition method. Deployment
still needs a concrete host/version, maintenance plan, backup and explicit
approval; these flags are not installed or activated by this implementation.

The default systemd unit deliberately omits `CAP_SYS_PTRACE`. The strict source
reader requires real process file/socket evidence and does not accept its older
configured-argument fallback. A deployment whose proc access is restricted must
remain unavailable. `packaging/examples/ovs-mgrd-witness.conf.example` documents
an optional, uninstalled capability profile; it also grants process access
beyond OVS and needs an explicit deployment security decision. No code enables
it automatically. Linux's process access checks are described in the
[kernel capability implementation](https://github.com/torvalds/linux/blob/v6.8/security/commoncap.c).

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

A held database connection monitors every persistent column in all four guarded
tables, settled counters and Interface ofport throughout both witness reads.
Any insert, delete or modification notification aborts. Notifications may be
coalesced, so a monitor is not independent ABA evidence. Atomic whole-table
waits still guard both images. A held standalone OVSDB journal is additionally
checked before and after the entire proof, including its device/inode, size,
mode/owner and mtime/ctime. Its actual DB peer must retain the open file, and
its actual executable must match the separately configured root-owned pin.
The flat-file header must identify standalone JSON storage; clustered/in-memory
storage, unsafe file permissions, append, truncation/restoration and
replacement/compaction reject this attempt. The journal is never parsed as
configuration or returned publicly. The qualified server logs and flushes
persistent changes before completion; ephemeral-only statistics are omitted.
This independently detects persistent ABA even when monitor updates coalesce.
See the pinned source
[file transaction encoding](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ovsdb/file.c)
and [log writes](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ovsdb/log.c);
the same paths are checked in the separately pinned 3.7.1 and 4.0.0 sources.
Routine statistics changes do not invalidate this execution-only monitor; legacy
readonly prepare preflights retain their conservative row-version checks.
Monitor semantics follow [RFC 7047 sections 4.1.5–4.1.6](https://www.rfc-editor.org/rfc/rfc7047.html#section-4.1.5).
The qualified OVS server flushes monitors before sending a transaction reply
(`ovsdb_jsonrpc_session_send` in each pinned `ovsdb/jsonrpc-server.c`); the
second atomic read therefore also forms a notification barrier.

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
composition cases and add 16 execution cases: all nine protocol endpoint
combinations except the disabled no-op (eight forward/compensation pairs), role
revocation, root revocation, field conflict, denied dispatch admission, RSTP
creation timer order, changed-after-image compensation refusal and persistent
ABA during witness collection without a next_cfg increment, and a rejected
database executable pin. Unit cases independently test journal append,
truncate/restoration and replacement without relying on monitor delivery. The same
tests run on all three OVS versions and both architectures. They also check
lost-reply target uncertainty, preservation of unrelated map values, raw leading
zeros and unset values. Execution fixtures include a real kernel system data
Port in addition to the local internal Port and check all five object bindings.
Unqualified positive multicast/protected-Port combinations remain blocked.

Manager repository tests separately cover the dedicated capability and old
credential ceiling, sealed operation, durable member reservations, confirmation
authority, management-loss compensation and no compensation replay. Existing
Safe Apply outage/restart/unknown-outcome matrices remain required. The existing
formal readonly/draft browser cases and the separate execution UI cases remain
required for every release. Source qualification passed both sets. Additional OVS 3.3.9
amd64/arm64 UI qualification uses the actual optional build and production
hardened services, first proving default source denial, then explicitly enabling
the temporary process-read profile to test responsive review, installed RSTP
confirmation and real management-path-loss compensation. The normal service
template is unchanged. Its three cases use the existing browser deadlines and
zero retry policy.

The wider #43 also includes Multicast, Tunnel, Mirror, QoS/Queue, exporters,
Isolation and Profile/Drift. This STP implementation does not complete those
domains or change their accepted Phase 1 scope. #71 remains deferred and is not
altered by this work.

## Source qualification record

Source `d07ad49a98c6a99bf99342ba720a32e2509ef2a2`, CI 39772835582: fourteen checks passed, thirteen official artifact
SHA-256 values verified. Each architecture retains all 315 previously accepted
top-level Go race cases and adds five (320 total), plus all 42 formal service
browser cases. Three exact OVS versions on both architectures retain the
default/enabled/sanitized matrices, 23 legacy native cases and 21 readonly
composition cases; each enabled/sanitized variant adds 16 execution cases.
Three actual OVS 3.3.9 UI cases per architecture passed; all 24 screenshots were
manually reviewed. Actual database executable pins match the native and UI
records and each build manifest.

The initial amd64 3.3.9 job failed during prerequisite installation because an
unrelated Microsoft package source returned HTTP 403, before tests or an
artifact existed. Attempt 2 reran that job and its dependent Gate. The other
twelve successful jobs retain identical execution times, steps and runner
identities. The original failure and all earlier superseded runs remain
separately retained. No authentication deadline or retry policy was relaxed;
#71 remains deferred. Independent main qualification is required before the
release tag and #23 closure. This record does not complete the other #43 domains.

Main `c7c05c1fc50d6c0e1a7ce78d764b85079f0a2c51` (CI 39782368345)
retained all thirteen official artifacts with verified digests. Twelve checks
passed; the 3.3.9 arm64 UI check failed. The native write reached Applied, but
the one-click confirmation received `TRANSACTION_VERSION_CHANGED`; the page
explicitly asked for refreshed review. Its unresolved transaction then correctly
protected the graph against the following test. The service journal and both
failure snapshots are retained. The new UI test now follows the same bounded
explicit-review flow as the existing formal suite: only an exact 409 plus
`command_effect=not-started` permits refreshing evidence and making a new human
decision. Unknown outcomes never enter this flow. Test deadlines, retries,
production confirmation logic and #71 are unchanged. A new full source and
independent main qualification are required.

The next source CI 39884753177 passed all six native witness jobs, including the
three execution UI cases on each architecture, but both full-runtime jobs failed
the existing draft browser case when its next validation used an old Candidate
revision (`CANDIDATE_CHANGED`). All thirteen official artifact digests and the
failed snapshots are retained separately. A delayed pre-acknowledgement read can
leave the previous Candidate's controls available after staging acknowledgement
while a fresh read is queued. A deterministic controller regression reproduces
this with the original implementation. After acknowledgement the controller now
invalidates older read tickets and withholds workspace/validation/transaction
controls until authoritative reads finish. The same regression passes without
replaying any mutation. The server's version admission and request-recovery
rules are unchanged; this does not diagnose or resolve #71.
