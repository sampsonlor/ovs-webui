# OVS WebUI

An engineering-focused web management experience for Open vSwitch (OVS), designed around safe configuration, explicit object relationships, operational evidence, and clear Standard/Expert modes.

## Project status

This repository contains the approved P0 low-fidelity UX baseline and the active P1 prototype. It is a product and interaction prototype, not production-ready switch-management software.

| Baseline                                          | Status                             |
| ------------------------------------------------- | ---------------------------------- |
| Architecture Baseline v1.0.1                      | Approved upstream reference        |
| Phase 1 Scope v1.0                                | Approved upstream reference        |
| UI Information Architecture + Page Inventory v1.0 | Approved Baseline                  |
| P0 Low-Fidelity UX Baseline v0.1                  | Frozen                             |
| P1 Low-Fidelity Prototype Plan v0.1               | Draft for Review                   |
| P1 Batch 01 · Bridge + Bond/LACP                  | Implemented · Review gate accepted |
| P1 Batch 02 · Diagnostics                         | Implemented · Review gate accepted |
| P1 Batch 03 · OpenFlow Viewer                     | Implemented · Draft for Review     |

The accepted Batch 01 path remains available:

`Switching overview → Bridges → Bridge detail → Add Bond → Candidate Workspace → Diff & Validation → Safe Apply → Evidence`

Batch 01 also exposes the required review states for drift, member down, LACP mismatch, validation failure, provider degradation, advanced native configuration, network loss, and unknown Apply outcome.

The accepted Batch 02 review path is:

`System Health or object deep link → Diagnostics catalog → bounded input → Job progress → structured result / export → Event / Audit evidence`

Batch 02 exposes catalog availability and permission states; valid, invalid, and over-broad input; queued, running, cancel-requested, cancelled, failed, partial, truncated, expired, unavailable, no-finding, no-data, provider-unavailable, operation-failed, and evidence-unavailable Job/result states. Every diagnostic remains predefined, scope-bounded, time-bounded, and output-bounded.

The current Batch 03 review path is:

`Switching → OpenFlow → authority / freshness context → bounded query → paginated flow snapshot → parsed or raw detail → bounded export`

Batch 03 keeps OpenFlow at Observe scope. It distinguishes a fresh complete result, external authority, stale data, truncation, a successful 0-row result, provider failure, permission denial, and query timeout. Standard Mode summarizes the pipeline; Expert Mode adds exact cookie, duration, raw output, and collection metadata without adding any write authority.

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

## Safety and scope

- Prototype data is synthetic.
- The current UI does not execute real OVS, host, or network mutations.
- Never commit credentials, production configuration, customer data, device exports, or diagnostic bundles.
- Expert Mode adds visibility and precision; it never bypasses authorization, validation, or safety gates.

## License

No open-source license has been selected yet. Public visibility does not by itself grant permission to use, modify, or redistribute this repository.
