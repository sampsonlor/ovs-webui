# OVS WebUI

An engineering-focused web management experience for Open vSwitch (OVS), designed around safe configuration, explicit object relationships, operational evidence, and clear Standard/Expert modes.

## Project status

See [current status and architecture alignment](docs/STATUS.md) for the
2026-09-07 inventory, source baselines, interaction fixes and review scope.

This repository contains the approved P0 low-fidelity UX baseline and the active P1 prototype. It is a product and interaction prototype, not production-ready switch-management software.

The approved production architecture specifies Go (`ovs-webd` / `ovs-mgrd`) and
a Svelte 5 static SPA. This React/Vinext prototype and Node-based local lab remain
interaction and contract review tools; they do not change that architecture.

| Baseline                                          | Status                             |
| ------------------------------------------------- | ---------------------------------- |
| Architecture Baseline v1.0.1                      | Approved source retained           |
| Phase 1 Scope v1.0                                | Approved upstream reference        |
| UI Information Architecture + Page Inventory v1.0 | Approved Baseline                  |
| P0 Low-Fidelity UX Baseline v0.1                  | Frozen                             |
| P1 Low-Fidelity Prototype Plan v0.1               | Draft for Review                   |
| P1 Batch 01 · Bridge + Bond/LACP                  | Implemented · Review gate accepted |
| P1 Batch 02 · Diagnostics                         | Implemented · Review gate accepted |
| P1 Batch 03 · OpenFlow Viewer                     | Accepted for integration           |
| Design System v0.1 + shared change control        | Accepted for integration           |
| Core Ports/VLAN API contract v0.1                 | Integration draft                  |

The accepted Batch 01 path remains available:

`Switching overview → Bridges → Bridge detail → Add Bond → Candidate Workspace → Diff & Validation → Safe Apply → Evidence`

Batch 01 also exposes the required review states for drift, member down, LACP mismatch, validation failure, provider degradation, advanced native configuration, network loss, and unknown Apply outcome.

The accepted Batch 02 review path is:

`System Health or object deep link → Diagnostics catalog → bounded input → Job progress → structured result / export → Event / Audit evidence`

Batch 02 exposes catalog availability and permission states; valid, invalid, and over-broad input; queued, running, cancel-requested, cancelled, failed, partial, truncated, expired, unavailable, no-finding, no-data, provider-unavailable, operation-failed, and evidence-unavailable Job/result states. Every diagnostic remains predefined, scope-bounded, time-bounded, and output-bounded.

The current Batch 03 review path is:

`Switching → OpenFlow → authority / freshness context → bounded query → paginated flow snapshot → parsed or raw detail → bounded export`

Batch 03 keeps OpenFlow at Observe scope. It distinguishes a fresh complete result, external authority, stale data, truncation, a successful 0-row result, provider failure, permission denial, and query timeout. Standard Mode summarizes the pipeline; Expert Mode adds exact cookie, duration, raw output, and collection metadata without adding any write authority.

The 2026-09-05 integration retains all three P1 batches and the Design System
branch. Bridge/Bond and VLAN use one Candidate and transaction lock. Diagnostics
append linked evidence without changing configuration intent; OpenFlow remains
Observe-only. See [integration review](docs/reviews/INTEGRATION_v0.1.md).

## P1 delivery order

1. Bridge + Bond/LACP
2. Diagnostics
3. OpenFlow Viewer
4. DPDK/Offload Observe
5. System Health
6. Capabilities

Each batch must pass its review gate before the next batch begins. Candidate Workspace, Diff, Safe Apply, Drift, Health, Event, and Audit remain shared cross-page workflows.

## Run locally

```bash
pnpm install
pnpm dev
```

Create a production build with:

```bash
pnpm build
```

## Repository layout

- `app/` — prototype application and interaction flows
- `components/` — reusable interface primitives
- `docs/baselines/` — approved UX baseline documents
- `docs/plans/` — prototype delivery plans under review
- `.openai/hosting.json` — existing private review deployment configuration

## Core workflow integration preparation

The current Design System and P0 workflow have state-level checks and a draft
Ports/VLAN API contract. See [the contract guide](docs/contracts/CORE_API_CONTRACT_v0.1.md)
and [acceptance scope](docs/reviews/CORE_WORKFLOW_ACCEPTANCE_v0.1.md).
The default UI uses synthetic session state. The opt-in local persistence lab
connects Ports, Candidate, server Diff/Validation and synthetic Safe Apply to a
disk-backed development service. Real OVS writes remain a separate provider gate.

The integration branch includes the [core HTTP adapter](docs/contracts/HTTP_ADAPTER_v0.1.md)
for Ports, Candidate and original-request recovery. It requires an authenticated
same-origin service and contract validator. The lab supplies a development-only
session provider and the generated runtime validator; production identity remains
a separate integration gate.

### Local Candidate persistence lab

```bash
pnpm dev:lab --port 3001
```

Use Editor A or Editor B to stage a VLAN change, then refresh or restart the
service to restore that user's Candidate. Read-only cannot stage. The observation
selector exercises stale/conflicting snapshots, provider loss and a node lock.
In Changes / Diff, run server validation and review its captured diff, checks and
job. Results survive restarts and expire when the Candidate, generation or policy
changes. Safety capabilities default to unavailable; the explicit synthetic
available fixture enables a reviewed local Safe Apply with a persisted checkpoint,
server deadline, confirmation, protected rollback and Event/Audit evidence.
Data is stored in ignored `.ovs-lab/state.sqlite`, relative to this checkout.
The lab uses Node's SQLite support (Node 22.13+; verified on Node 24), binds to
loopback, and is absent from the hosted production build.

See [the local integration guide](docs/contracts/LOCAL_PERSISTENCE_v0.1.md) for
review steps, automatic checks and the remaining backend scope.
See [server validation review](docs/contracts/LOCAL_VALIDATION_v0.1.md) for
expiry, permission changes, request recovery and safety prerequisites.
See [Safe Apply review](docs/contracts/LOCAL_SAFE_APPLY_v0.1.md) for confirmation,
crash recovery, rollback conflicts and the synthetic executor boundary.

```bash
pnpm test
pnpm contracts:generate
pnpm contracts:check
```

Edit `contracts/core-v0.1.mjs`, then regenerate the OpenAPI document and TypeScript
types. The test command rejects stale generated files and checks response fixtures,
lost acknowledgements, safe confirmation, rollback conflicts and out-of-order reads.

## GitHub CI and isolated testing

Track accepted work and upcoming slices on the public
[OVS WebUI Phase 1 board](https://github.com/users/sampsonlor/projects/2/views/1).

Pull requests and main updates run the pinned toolchain, contract checks, product
lint, TypeScript, regression tests, process-recovery integration tests and production
build. `CI Gate` requires both jobs to succeed. JUnit reports are retained for 7 days.
The `ci-integration` environment uses disposable Linux runners and per-test SQLite
databases; it has no production credentials or real OVS executor.

See [the GitHub CI guide](docs/contracts/GITHUB_CI_v0.1.md) for local commands,
lint scope, environment isolation and remaining Staging/browser/provider gates.

## Safety and scope

- Prototype data is synthetic.
- The current UI does not execute real OVS, host, or network mutations.
- Never commit credentials, production configuration, customer data, device exports, or diagnostic bundles.
- Expert Mode adds visibility and precision; it never bypasses authorization, validation, or safety gates.

## License

No open-source license has been selected yet. Public visibility does not by itself grant permission to use, modify, or redistribute this repository.
