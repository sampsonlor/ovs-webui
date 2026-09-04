# OVS WebUI

An engineering-focused web management experience for Open vSwitch (OVS), designed around safe configuration, explicit object relationships, operational evidence, and clear Standard/Expert modes.

## Project status

This repository currently contains the approved P0 low-fidelity UX baseline and the planning baseline for P1 prototype work. It is a product and interaction prototype, not production-ready switch-management software.

| Baseline                                          | Status                      |
| ------------------------------------------------- | --------------------------- |
| Architecture Baseline v1.0.1                      | Approved upstream reference |
| Phase 1 Scope v1.0                                | Approved upstream reference |
| UI Information Architecture + Page Inventory v1.0 | Approved Baseline           |
| P0 Low-Fidelity UX Baseline v0.1                  | Frozen                      |
| P1 Low-Fidelity Prototype Plan v0.1               | Draft for Review            |

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
The UI continues to use synthetic session state; these interfaces do not enable
real switch operations or server persistence.

```bash
pnpm test
pnpm contracts:generate
pnpm contracts:check
```

Edit `contracts/core-v0.1.mjs`, then regenerate the OpenAPI document and TypeScript
types. The test command rejects stale generated files and checks response fixtures,
lost acknowledgements, safe confirmation, rollback conflicts and out-of-order reads.

## Safety and scope

- Prototype data is synthetic.
- The current UI does not execute real OVS, host, or network mutations.
- Never commit credentials, production configuration, customer data, device exports, or diagnostic bundles.
- Expert Mode adds visibility and precision; it never bypasses authorization, validation, or safety gates.

## License

No open-source license has been selected yet. Public visibility does not by itself grant permission to use, modify, or redistribute this repository.
