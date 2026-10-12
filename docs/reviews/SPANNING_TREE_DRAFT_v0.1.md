# STP / RSTP draft preparation review gate

Scope: [M2b preparation → sealed Candidate → Diff / Validation](../implementation/SPANNING_TREE_DRAFT_v0.1.md). #23 and #43 remain Open / In Progress. The #71 investigation remains Open / Pending.

| Review path | Required result |
| --- | --- |
| Independent authority | Exact root-granted immutable Bridge and dedicated current capability are required. Other domain grants and schema mutability do not authorize preparation. |
| Normal draft | Two flags and seven raw/native-unset parameters have a distinct manager-captured original and proposed image; stage/validate leave live OVS unchanged. |
| Strict scope and payload | Unsupported graph types, advanced parameters, foreign operation fields, inactive edits and RSTP hello time are rejected or unavailable; private graph data is withheld. |
| Native validation | Range/relations, inactive settings, RSTP rounding and STP hello-time caveats remain blocked where required; no default, correction or runtime proof is fabricated. |
| Drift and replacement | Field/dependency/schema/generation/identity changes require discard and fresh review. Restage/Rebase cannot replace the original or transfer a grant to a same-name replacement. |
| Revocation and permissions | Root/capability revocation invalidates the draft and review; old credential ceilings cannot expand. Withheld configuration exposes no preparation-authority oracle. |
| Unknown / stale / provider degradation | Preparation is unavailable; previously observed values do not become current authorization or installed proof. |
| Standard / Expert | Same preparation and blocking behavior; Expert adds identifiers. Findings in Standard are readable explanations. |
| Responsive / keyboard | Only desktop prepares new drafts. Tablet/mobile review with no staging form. Labels, focus, theme and the existing responsive evidence remain usable. |
| Execution boundary | Every Validation contains `SPANNING_TREE_EXECUTION_PENDING`, remains unusable and cannot enter Safe Apply, including with the safety service enabled. Succeeded Job means review completed only. |

Required local checks: contract generation/compatibility, type/lint/unit/client checks, Go package tests and production build. Required delivery checks: all eight CI jobs on the exact source and merged main, amd64/arm64 native race and complete browser suites, all three real OVS schema fixtures, unchanged native execution/recovery and diagnostic gates, digest-verified artifacts and visual review of new plus retained spanning-tree evidence. Failures and repaired attempts are retained in the delivery record.

The delivery PR and annotated tag record the exact tested revisions, CI runs, report counts, artifact hashes and visual disposition. This gate accepts draft review only; STP/RSTP native writes, installed timers, transitions, compensation, Basic Manage and Phase 1 completion remain pending.
