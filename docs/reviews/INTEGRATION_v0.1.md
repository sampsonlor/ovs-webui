# Integrated prototype v0.1

Disposition: the user accepted the current work and requested all branches merged
into `main` on 2026-09-05. This records prototype integration, not production or
architecture approval.

## Included branches

| Branch                                | Source head | Included behavior                                                                 |
| ------------------------------------- | ----------- | --------------------------------------------------------------------------------- |
| feat/p1-batch-01-bridge-bond          | 7dc1834     | Bridge and Bond/LACP review, native object identity and staged intent             |
| feat/p1-batch-02-diagnostics          | f4e9c38     | Bounded diagnostic catalog, Job/result states and evidence links                  |
| feat/p1-batch-03-openflow-viewer      | 342f000     | Observe-only OpenFlow queries, authority, freshness and bounded export preview    |
| feat/design-system-core-contract-v0.1 | f338bbc     | Ports design system, shared transaction control and draft Ports/VLAN API contract |

The three P1 branches are cumulative. Merging Batch 03 also incorporates Batch 01
and Batch 02 history. The Design System shell is retained and its existing
Switching and Operations entries now reach the P1 pages.

## Conflict resolution

- Bridge and Bond use the same Candidate, revision validation, reason, active
  transaction lock, confirmation deadline, unknown-outcome recovery and evidence
  as VLAN. Bond remains a Port, with its parent Bridge shown in preflight.
- An overlapping Bridge/Bond conflict requires an authoritative native snapshot.
  The prototype offers keeping current configuration; it does not invent a
  three-way native snapshot or offer a force overwrite. A non-overlapping stale
  fixture rebases intent and invalidates validation.
- Diagnostic Jobs remain separate from configuration transactions. Their linked
  Audit/Event entries append to shared evidence and never unlock an uncertain
  configuration operation. Fixed Job and correlation IDs denote review fixtures.
- Desktop supports new configuration; tablet supports review and bounded
  diagnostics; mobile retains incident summaries and existing Safe Apply handling.
- Standard/Expert changes detail, not authority. OpenFlow has no write surface.
  Existing P1 local visual treatments are retained for later component migration.

## Validation and remaining scope

Focused checks cover cross-object replacement, shared transaction ownership,
topology evidence identity, stale/conflict behavior, disconnected expiry,
diagnostic isolation, programmatic entry-point scope and device limits. The
existing state and generated-contract checks are retained. All 35 tests, TypeScript,
scoped lint and the production build passed. Browser interaction has not been re-run
for this integration.

All operations remain synthetic and reset on reload. Bridge/Bond inventory stays
a representative fixture even after a simulated confirmation. The HTTP contract
currently describes Ports/VLAN only: native Bridge/Bond mutation payloads, real
authentication, persistent per-user Candidate storage, OVS providers and a
server-owned Safe Apply watchdog still require backend integration. No hosted
deployment or access policy is changed by this merge.

The next core integration slice is the typed HTTP boundary for Ports reads and
per-user Candidate loading/staging, followed by validation and authoritative
transaction recovery. An actual server must supply identity and persistence;
client session state must never be presented as either.
