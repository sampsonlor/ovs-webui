# Repository guidance

## Purpose

Build and review the OVS WebUI interaction prototype while preserving the approved architecture, Phase 1 scope, information architecture, and frozen P0 workflow.

## Product invariants

- Model Bridge -> Port -> Interface explicitly. A Bond is a Port with member Interfaces.
- Route configuration changes through Candidate Workspace -> Diff/Validation -> Apply or Safe Apply -> Event/Audit evidence.
- Never add direct live-save behavior for Bridge, Bond, VLAN, or other switching configuration.
- Treat Safe Apply, rollback, drift, health, events, jobs, and audit as shared resources rather than page-local state.
- Standard and Expert modes change information depth, not permissions or validation.
- Represent unavailable, unknown, stale, externally controlled, and provider-degraded states explicitly.
- Keep OpenFlow, DPDK, and Offload within their approved Observe or conditional capability boundaries.

## Delivery sequence

Implement P1 in this order and do not begin a later batch before the current review gate is accepted:

1. Bridge + Bond/LACP
2. Diagnostics
3. OpenFlow Viewer
4. DPDK/Offload Observe
5. System Health
6. Capabilities

## Interaction and responsive expectations

- Desktop is the complete review and configuration surface.
- Tablet supports Standard-mode review, recommended diagnostics, and handling an existing Safe Apply.
- Mobile is an incident companion; do not introduce new high-risk transactions there.
- Every clickable prototype action must produce visible state, feedback, or navigation.
- Preserve keyboard access, readable focus states, and meaningful labels.

## Engineering workflow

- Use `pnpm` and preserve `pnpm-lock.yaml`.
- Run `pnpm build` before presenting a completed prototype batch.
- Keep synthetic fixtures free of personal, customer, credential, and production-network data.
- Use focused feature branches such as `feat/p1-batch-01-bridge-bond`.
- Keep `main` in a reviewable state and record accepted baselines with annotated tags.
- Do not change `.openai/hosting.json` or site access unless the task explicitly requires a deployment change.

## Review evidence

For each batch, retain the normal path, required exception states, Standard/Expert comparison, responsive responsibilities, and the corresponding review disposition. A visual-only happy path is not sufficient when the plan calls for drift, validation failure, provider degradation, or OutcomeUnknown behavior.
