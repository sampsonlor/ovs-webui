# Documentation index

Start with [current status](STATUS.md), the
[Phase 1 implementation design](implementation/PHASE1_IMPLEMENTATION_DESIGN_v0.1.md)
and its [review disposition](reviews/PHASE1_DESIGN_v0.1.md), currently Proposed.
The [six-batch integration review](reviews/INTEGRATION_v0.2.md) was accepted and
merged in PR #18. The [53-page approved IA coverage inventory](reviews/P1_IA_COVERAGE_v0.1.md)
records that baseline; [shared inventory PR #19](https://github.com/sampsonlor/ovs-webui/pull/19)
remains a separate pending review.
The [Capabilities review](reviews/CAPABILITIES_v0.1.md) is accepted in PR #17,
tagged `prototype-capabilities-v0.1` at `862b19a`.
The accepted [CI baseline](reviews/CI_BROWSER_BASELINE_v0.1.md) records PR #16
and the closure of issues #6 / #7.
The [2026-09-07 interaction review](reviews/UI_WORKFLOW_REVIEW_v0.2.md) and
[approved IA navigation alignment](reviews/NAVIGATION_ALIGNMENT_v0.1.md)
were accepted and merged in PR #9.
Older records below describe their own review date and implementation slice.

## Phase 1 formal implementation design v0.1

- [Implementation design](implementation/PHASE1_IMPLEMENTATION_DESIGN_v0.1.md) — proposed processes, IPC, dual databases, identity, authorization, execution and recovery
- [Scope and IA traceability](implementation/PHASE1_SCOPE_TRACEABILITY_v0.1.md) — all 58 Scope IDs, 53 Page IDs, release deliverables and acceptance owners
- [Machine-readable mapping](implementation/phase1-traceability.v0.1.json) — source hashes and per-requirement responsibilities
- [ADR 0001](adr/0001-phase1-runtime-contracts.md) — proposed runtime, driver and protocol choices
- [API migration](contracts/PHASE1_API_MIGRATION_v0.1.md) and [core OpenAPI proposal](../contracts/proposals/phase1-v1.openapi.json) — future REST contract; current lab remains a reference
- [Design review](reviews/PHASE1_DESIGN_v0.1.md) — document/contract checks, unresolved approval and subsequent implementation gates

This work belongs to #30, within the full Phase 1 delivery. Receiving a Scope
draft does not approve that draft or mark formal services implemented.

## Core workflow and integration contract v0.1

- `reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md` — state-level acceptance evidence and remaining browser/backend gates
- `contracts/CORE_API_CONTRACT_v0.1.md` — API semantics, state mapping, recovery and integration sequence
- `contracts/LOCAL_PERSISTENCE_v0.1.md` — local Ports/Candidate persistence and session review
- `contracts/LOCAL_VALIDATION_v0.1.md` — durable server diff, validation jobs, expiry and exception review
- `contracts/LOCAL_SAFE_APPLY_v0.1.md` — synthetic transactions, protected rollback, recovery and evidence
- `contracts/GITHUB_CI_v0.1.md` — CI gates, isolated Linux testing and project workflow
- `reviews/CI_BROWSER_BASELINE_v0.1.md` — full lint and browser workflow coverage for issues #6 / #7
- `../contracts/openapi.v0.1.json` — generated OpenAPI contract for the first Ports/VLAN integration slice

The contract is a review proposal. The opt-in local lab connects HTTP adapters to synthetic persistent resources. Production persistence, authentication and OVS operations remain separate integration gates.

## Design System v0.1 review

- `reviews/CODE_BASELINE_AUDIT_v0.1.md` — code inventory, architecture alignment, exception coverage, validation and remaining gaps
- `design-system/DESIGN_SYSTEM_v0.1.md` — Ports foundations, shared change-control components and review paths

These are review drafts extending the P0 code baseline; they do not approve a new Architecture/Phase 1 baseline or a P1 batch gate.

## Baselines

- `baselines/OVS_WebUI_Architecture_Baseline_v1.0.1.docx` — Approved Baseline; original received on 2026-09-07
- `baselines/OVS_WebUI_Phase1_Scope_v1.0.docx` — original received on 2026-09-09; its own status is Draft for Review
- `baselines/OVS_WebUI_UI_Information_Architecture_Page_Inventory_v1.0.docx` — Approved Baseline

## Plans

- `plans/OVS_WebUI_P1_Low_Fidelity_Prototype_Plan_v0.1.docx` — Draft for Review

The received Scope draft is mapped in full, with differences explicitly recorded
against approved Architecture and IA boundaries. A Draft for Review IA file supplied
at the repository root does not supersede the retained Approved Baseline.
