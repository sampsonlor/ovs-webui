# Spanning-tree fenced configuration preflight v0.1

This M2c3 slice integrates the private execution authority and runtime-source
prerequisites into `Executor.ReadSpanningTreePreflight`. It reads a reviewed
prepare draft against its exact immutable Bridge binding, schema, original and
dependency. It grants no execution authority and has no HTTP, IPC or CLI caller.
The prepare operation remains unconditionally `SPANNING_TREE_EXECUTION_PENDING`.

## Read boundary

1. Resolve the current dedicated execution permission before inventory/provider
   access. Built-in roles and old credential ceilings remain unchanged. Require
   both independent root policies and the conservative system Bridge graph.
2. Check the complete nine-field reviewed original, closed before/after images,
   immutable management ID/UUID/generation, schema and dependency. Derive the
   single name/protocol from private inventory; no caller-supplied runtime name,
   raw transaction or shell command is accepted.
3. Hold one DB connection, checking the pinned peer/schema/file. In a single
   read-only transaction, zero-timeout guards compare all persistent columns and
   exact row sets of Open_vSwitch, Bridge, Port and Interface. Require a single
   root and exact equal nonnegative next_cfg/cur_cfg. Four fixed selects capture
   every present row's `_uuid` and `_version` in the same transaction.
4. Read exactly one native STP/RSTP sample through the existing qualified source
   checker (trusted executable, process incarnations, real established database
   connection, root/schema/file and receive timestamp). Recheck current permission
   and private inventory scope before each source collection and on return.
5. Execute the identical fixed native fence on the same held connection. Compare
   exact version maps, peer/file and current private scope. Compare the installed
   local original and both timer directions using the existing pure assessment.
   Return only if all checks pass; otherwise return an entirely empty result.

The entire operation has a two-second context/deadline, installed before schema
I/O, with cancellation closing the held connection. There is no reconnect,
polling, retry, cache/default fallback or write transaction. Native request data
is capped at 256 KiB, version result at 512 KiB and aggregate rows at 2048;
existing strict wire/JSON budgets still apply. Unknown native errors become a
static reason without host paths or raw database details. General discovery
still rejects transact; only this locally compiled wait/select sequence is sent.

Each valid replacement of either spanning-tree root policy changes a private
revision, including revoke/regrant to an identical set. Invalid replacements do
not change the policy. The inventory view checks that revision around projection;
all preflight callbacks compare it across I/O. This prevents a restored allowlist
from resurrecting an earlier in-flight scope. Public prepare policy/hints remain
unchanged; the new revision only enters private execution fingerprints.

## Meaning and limits

The result correlates a qualified name-scoped sample with an immutable reviewed
**configuration** and unchanged versions of rows present at the first native
fence. `_version` detects a changed-and-restored row even when its values and
next_cfg are unchanged. Additions/removals visible at either atomic fence fail.
Ephemeral/status changes also change versions and therefore reject conservatively;
there is no retry to conceal a busy or unstable source. This can reduce availability.
A row inserted and deleted entirely between fences without modifying a retained
row is outside this evidence. These are bounded point-in-time reads, not a lock
or a promise that the state remains unchanged after return.

Equal cur_cfg/next_cfg is a necessary settled-configuration check, not proof that
this daemon owns the IDL lock, that every native setter installed the desired
configuration, that the inactive protocol is absent, or that unixctl's Bridge
name corresponds to an independently exposed installed UUID. The existing
source connection witness alone cannot prove those facts. The result has no
Applied flag, execution plan, durable seal, graph lease/reservation, commit marker
or dispatch authority. A compatible transition remains analysis only.

Remaining work: authenticate a separate sealed execution scope; integrate durable
graph reservations and rechecks at dispatch; qualify installed Bridge identity,
exclusive protocol and IDL ownership; compile guarded writes and precise unset
compensation; add High-risk Safe Apply, crash/restart/lost-reply/OutcomeUnknown and
protocol transition acceptance. Do not convert the prepare operation into writes.
Basic Manage, Phase 1 and #23/#43 remain unfinished. #71 stays deferred unchanged.

## Verification

Five new Go regression entry points cover policy revoke/regrant, exact fixed
read-only fences, incomplete/settling/budget failures, malformed/partial/foreign
version replies, authorization before provider access and immutable scope identity.
A separate native group has 16 scenarios per schema on amd64 and arm64. Positive
cases use the actual kernel system Bridge for STP and RSTP, independent direct
DB inventory and current explicit credentials. It also checks old ceilings,
prepare-only root grant, reviewed schema/dependency/original/generation/closed
image mismatch, post-runtime role withdrawal, both root grant ABA cases, real
configuration ABA without cfg-counter mutation, cancellation, missing control
socket and same-name Bridge UUID replacement. Every failure withholds all fields.
The existing source, runtime, parameter, draft, topology, write/recovery,
authentication, diagnostic and browser gates remain mandatory and unchanged.

No front-end, public contract, browser test, daemon service capability or timeout
changes are included. Visual review of retained browser evidence is scoped as
spot inspection, not exhaustive manual inspection of every screenshot.

## Primary sources

- [RFC 7047 section 3.2](https://datatracker.ietf.org/doc/html/rfc7047#section-3.2):
  immutable row UUID and ephemeral per-change row version semantics.
- [RFC 7047 sections 5.2.2 and 5.2.6](https://datatracker.ietf.org/doc/html/rfc7047#section-5.2.6):
  explicit select fields and zero-timeout wait transactions.
- [OVS 3.3.9 Bridge implementation](https://github.com/openvswitch/ovs/blob/v3.3.9/vswitchd/bridge.c):
  name-based Bridge/runtime lookup and asynchronous configuration processing;
  counters alone do not establish all installed/ownership prerequisites.
- [Source/authority boundary](SPANNING_TREE_SOURCE_AUTHORITY_v0.1.md) and
  [runtime/transition boundary](SPANNING_TREE_RUNTIME_PREFLIGHT_v0.1.md).
