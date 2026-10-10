# STP/RSTP validator · M2a review gate

Scope: [seven native basic Bridge parameters and protocol mutual exclusion](../implementation/SPANNING_TREE_VALIDATOR_v0.1.md). Parent #23 and #43 remain open. The #71 investigation remains in Pending.

## Required evidence and disposition

| Review path | Required result |
| --- | --- |
| Normal configuration | Both protocol defaults and native range boundaries pass basic checks; observed unset values remain unset. |
| Native caveats | Real OVS demonstrates STP timer clamping, RSTP priority rounding and previous-timer retention; the original configuration is visible and flagged for review. |
| Inactive settings | Invalid inactive STP timers remain visible while RSTP is enabled. The check scope is explicit. |
| Unknown / schema | Missing, malformed-presence and unsupported source fields cannot produce a passing assessment. |
| Permission denial | Withheld configuration has no validity/check oracle. State/inventory denial remains enforced by the service. |
| Stale / service outage | Stale retains observations but has no current assessment; service outage retains the existing unavailable state. |
| External control | Valid parameter values grant no ownership or editable operation. |
| Standard / Expert | Same findings and permissions; only Expert adds code, rule version and scope detail. |
| Responsive / keyboard | All three viewports can review; no new transaction exists. Existing keyboard links, theme and overflow checks remain required. |
| Execution boundary | Pending spanning-tree Candidate intent is rejected; existing native writes and recovery gates remain unchanged. |

Local verification includes contract compatibility, unit/client tests, Go package tests, type/lint checks and the production build. Linux amd64 and arm64 must each pass the complete existing CI matrix, the new real OVS parameter checks on three database schemas and the formal browser suite. A schema fixture is not a claim about the runtime release; each report retains its actual OVS version.

CI artifact digests, exact source and merged main revisions, screenshot review and final acceptance disposition are recorded in the delivery PR and annotated `phase1-spanning-tree-validator-v0.1` tag. Feature and main CI must both pass before this gate is accepted. Failed attempts, if any, remain part of the delivery record.

M2a acceptance does not accept Basic Manage, installed timer/priority proof, safe transition ordering, write authority or compensation. Those gates are enumerated in the implementation document. It does not close #23 or #43 or complete Phase 1.
