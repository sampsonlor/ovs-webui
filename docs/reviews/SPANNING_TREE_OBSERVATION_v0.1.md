# SW-10 / #43 M1 review

Date: 2026-10-10. Scope: formal STP/RSTP observation only. Implementation and deferred write gates: [M1 design](../implementation/SPANNING_TREE_OBSERVATION_v0.1.md).

## Required evidence

| Gate | Evidence / disposition |
| --- | --- |
| G1 / G2 | Generated API/DTO compatibility, typed native schema gates, configuration/runtime separation, withholding, unrecognized values, bounded detail and immutable identity tests. Exact source head and results retained in the delivery receipt. |
| G3 / G4 | `tests/daemon/spanning_tree.py` in the existing real HTTPS → webd → Unix IPC → mgrd → OVSDB/ovs-vswitchd inventory fixture, schemas 3.3.9 / 3.7.1 / 4.0.0 on amd64 / native arm64. All read assertions required; fixture mutations are not product writes. |
| UX | Formal browser cases cover native RSTP intent/runtime, unknown/unset, mutual-enable conflict, external ownership, configuration observer, denied inventory, empty results, retired identity, stale provider and manager outage. Screenshots are retained in the runtime artifacts and manually reviewed before acceptance. |
| Standard / Expert | Both expose configured protocol, basic Bridge parameters and Port runtime. Expert adds per-field provenance, advanced native values, identity and explicit write gate. Same permission and observation boundaries. |
| Responsive / keyboard | Desktop supports the full observation page; tablet reviews the same evidence; mobile remains an incident companion with no new configuration transaction. Focusable links, labelled Bridge filter and no page-wide horizontal overflow are verified. Light/dark evidence retained. |
| Native recovery / security | Existing Safe Apply, crash, schema, transport, permission and redaction suites remain required. M1 adds no spanning-tree writer and claims no new rollback/OutcomeUnknown implementation. These stay M2 gates. |

## Acceptance disposition

Accept M1 only after the exact feature head's required checks pass, native/browser reports and screenshots are inspected, and the same code tree independently passes main CI. Keep both #23 and #43 open. Preserve the accepted M1 baseline with an annotated tag; remove the completed feature branch after verification. Acceptance identifiers and final evidence are recorded on #43 and the delivery PR rather than predicting CI results in this source document.

Deferred work is explicit: native Mirror output resolution and unverified types; all STP/RSTP configuration admission, executable validators, native Applied proof, guarded compensation, loss/restart recovery and full #23 completion; the remaining #43 domains and full Phase 1 release gates. #71 stays Pending and its historical root-cause criteria are not accepted by these tests.
