# Documentation index

Start with [current status](STATUS.md) and the
[2026-09-07 interaction review](reviews/UI_WORKFLOW_REVIEW_v0.2.md).
Older records below describe their own review date and implementation slice.

## Core workflow and integration contract v0.1

- `reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md` — state-level acceptance evidence and remaining browser/backend gates
- `contracts/CORE_API_CONTRACT_v0.1.md` — API semantics, state mapping, recovery and integration sequence
- `contracts/LOCAL_PERSISTENCE_v0.1.md` — local Ports/Candidate persistence and session review
- `contracts/LOCAL_VALIDATION_v0.1.md` — durable server diff, validation jobs, expiry and exception review
- `contracts/LOCAL_SAFE_APPLY_v0.1.md` — synthetic transactions, protected rollback, recovery and evidence
- `contracts/GITHUB_CI_v0.1.md` — CI gates, isolated Linux testing and project workflow
- `../contracts/openapi.v0.1.json` — generated OpenAPI contract for the first Ports/VLAN integration slice

The contract is a review proposal. The opt-in local lab connects HTTP adapters to synthetic persistent resources. Production persistence, authentication and OVS operations remain separate integration gates.

## Design System v0.1 review

- `reviews/CODE_BASELINE_AUDIT_v0.1.md` — code inventory, architecture alignment, exception coverage, validation and remaining gaps
- `design-system/DESIGN_SYSTEM_v0.1.md` — Ports foundations, shared change-control components and review paths

These are review drafts extending the P0 code baseline; they do not approve a new Architecture/Phase 1 baseline or a P1 batch gate.

## Baselines

- `baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx` — Approved Baseline; original received on 2026-09-07
- `baselines/OVS_WebUI_UI_Information_Architecture_Page_Inventory_v1.0.docx` — Approved Baseline

## Plans

- `plans/OVS_WebUI_P1_Low_Fidelity_Prototype_Plan_v0.1.docx` — Draft for Review

Phase 1 Scope v1.0 remains an authoritative upstream reference whose standalone
approved source is still missing. Architecture §11 supplies scope constraints
while that original is being recovered. A Draft for Review IA file supplied at
the repository root does not supersede the retained Approved Baseline.
