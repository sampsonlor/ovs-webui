# STP/RSTP M2c2: independent authority and daemon/database source

Status: private execution prerequisites; **no production caller, public operation,
CLI execution/control flag, native write, Applied result or Safe Apply adapter**.
The public API remains 1.27.0. `bridge.spanning-tree.prepare` retains its
unconditional `SPANNING_TREE_EXECUTION_PENDING` blocker. Legacy
`spanning_tree.configure` remains unsupported. #23/#43 remain open.

## Independent authority

The reserved `ovs.bridge.spanning-tree.write` capability is recognized for
explicit role assignment. It is deliberately absent from all builtin role
templates during this prerequisite, including Administrator and NetworkAdmin.
Existing credentials retain their original capability ceiling. The private
`SpanningTreeExecutionAuthorizer` resolves current claims on every invocation and
requires inventory/configuration reads, workspace write, configuration apply and
the new domain capability. Preparation and other domain capabilities do not
substitute it. A callback never retains the bearer in provider state or evidence.

Inventory has a separate, default-empty root-owned execution Bridge allowlist,
limited to 128 unique immutable management IDs. Invalid replacement is atomic and
leaves the prior policy intact. No public or CLI setter is exposed in this batch.
`SpanningTreeExecutionView` checks this grant before projecting configuration,
rechecks it under the lock and requires an exact Bridge UUID/management ID/current
generation binding plus all existing preparation configuration/schema/graph and
independent preparation-authority gates. A new same-name object cannot inherit
the old identity's grant. The execution policy enters the private returned policy
fingerprint; public preparation hints remain draft-only.

This is scope admission, not a graph reservation, sealed execution DTO or proof
that the view is still current after arbitrary I/O. Future callers must combine
current authorization, coherent graph views and reservations at every required
execution/compensation boundary.

## Bounded read-only process/database attestation

`ReadBoundSpanningTreeRuntime` wraps the accepted M2c1 fixed native reader. It
requires a nonempty expected control daemon incarnation, a root-supplied absolute
executable path and a fresh private OVSDB inventory evidence image. No command,
executable, endpoint or evidence is accepted through HTTP/IPC.

Before provider access, before source collection, after the runtime response and
before returning a sample, its required authorization callback resolves current
permission. A denied or unavailable check returns an entirely empty sample.
The sample timestamp is captured when the native wire reply is received; later
source/authorization checks cannot refresh the age of that runtime observation.
The reader's two-second context bounds control/source I/O and cancellation closes
both control and database connections. No shell, appctl process, discovery,
write/lock/reconnect operation, automatic retry or cached replacement is added.

Both before and after the one `stp/show` or `rstp/show` request:

1. Verify control UID/PID/start and the pinned boot/incarnation. Require the
   actual `/proc/PID/exe` inode to match the explicitly configured regular,
   root-owned executable with execute permission and no group/world write bit.
2. Dial the configured OVSDB socket and verify the observed server UID and exact
   PID/start, endpoint and boot against fresh manager inventory evidence. Read the
   schema and compare its exact digest. A fixed read-only select requires exactly
   one Open_vSwitch root matching the observed immutable root UUID.
3. Require the database server's actual open file handle to match the configured
   file inode/device and existing journal witness. The inventory provider's
   process-argument fallback is insufficient here.
4. Enumerate at most 1,024 file descriptors in each process. Use Linux
   `NETLINK_SOCK_DIAG` / `UNIX_DIAG_PEER` to require exactly one bidirectional,
   established stream socket pair held by these two processes. Equal UIDs,
   process argv and matching paths cannot substitute a live connection.
5. Recheck both process incarnations. The control/DB PID/start, executable and
   DB file device/inode and connection pair must remain identical across the
   runtime request. Daemon, database or connection replacement invalidates the
   sample.

The diagnostic dump has a one-second ceiling inside the enclosing context,
100-ms receive waits, 64-KiB datagrams, a 1-MiB total budget and at most 4,096
stream records. Kernel sender, sequence, message/attribute bounds, interruption,
truncation, errors and duplicate records are checked. Restricted proc/diagnostic
access fails closed. No new service capability, ptrace privilege or weaker
fallback is introduced. Existing hardened deployments may therefore remain
unqualified; future deployment integration must retain that explicit boundary.

## Limits and remaining integration

These two private prerequisites are deliberately separate. The process/database
reader does **not** claim that a name-based runtime response identifies an
immutable Bridge UUID. It does not establish IDL lock ownership, protocol
exclusivity, no ABA replacement, current graph reservations or actual installed
configuration for dispatch/Applied/compensation. A live socket pair alone cannot
establish those facts. The inventory method supplies a separately qualified
immutable configuration scope; this batch has no caller that combines and fences
them into an executable plan.

Remaining M2c work: integrate those boundaries with guarded native reads and
sealed execution scope/graph reservations; independently prove the installed
Bridge and exclusive protocol; compile guarded writes preserving unrelated keys
and exact unset originals; implement High-risk Safe Apply and guarded
compensation with both timer directions; accept crash/restart/lost reply,
OutcomeUnknown, provider degradation, conflicts and protocol transitions. Neither
`compatible` nor a returned sample can bypass the existing pending execution
blocker. Basic Manage and Phase 1 remain incomplete. #71 stays deferred.

## Acceptance

Seven new Go regression entry points cover independent root authority, immutable
binding/graph/provider failures, explicit current capability and credential
ceiling, strict diagnostic parsing, real kernel socket peer relations and
authorization before provider access and the fixed read-only root probe. A separate native CI group exercises 13
cases against each of the three schema fixtures on amd64 and arm64, including two
real OVS instances with the same Bridge name, a same-UID relay, changed root,
schema, process, file and timestamps, explicit roles/old credentials, wrong
executable/incarnation and post-read revocation. Dummy datapaths qualify the
read-only source test only, never the conservative production execution graph.
All previously accepted runtime/parameter/draft, write/recovery, browser and
diagnostic gates remain mandatory. No UI code/layout changes are included.

## Primary sources

- [Linux sock_diag(7)](https://man7.org/linux/man-pages/man7/sock_diag.7.html):
  established Unix socket peer inode attributes and diagnostic framing.
- [Linux Unix diagnostic ABI](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/include/uapi/linux/unix_diag.h):
  request/response layouts, UDIAG_SHOW_PEER and UNIX_DIAG_PEER.
- [OVS 3.3.9 daemon](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/ovs-vswitchd.c)
  and [Bridge IDL](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/bridge.c):
  daemon/control startup, database remote/IDL and separate IDL lock ownership.
- [M2c1 runtime/source mapping](SPANNING_TREE_RUNTIME_PREFLIGHT_v0.1.md):
  fixed read methods, local versus operational timers and bidirectional limits.
