# Formal Svelte Ports/VLAN review

2026-09-21 · #41 · delivery: [PR #67](https://github.com/sampsonlor/ovs-webui/pull/67). Acceptance requires the final PR head and merged main to pass all six CI jobs. The PR's delivery record and annotated `phase1-svelte-ports-vlan-v0.1` tag record the accepted commit; intermediate runs do not constitute acceptance.

## Required evidence

| Review surface | Evidence and expected result |
| --- | --- |
| Normal authenticated workflow | Real local account → Go-served static SPA → OVS Ports → VLAN Candidate → three-way Diff → Validation → Applied/healthy → explicit Safe Apply confirmation → durable Job and correlated Audit |
| Lost response and refresh | Browser receives no real admission response; original request remains persisted; refresh + GET receipt restores the same transaction; exactly one POST; concurrent tabs serialize receipt recovery |
| External conflict | A separate real `ovs-vsctl` writer changes the target field; UI blocks validation and requires snapshot-bound keep-current / keep-mine review; a newer snapshot clears prior choices |
| Provider and manager failures | Actual OVSDB process outage yields stale inventory and read-only fields; stopped manager yields unavailable authorization with no invented values |
| Permission changes and late response | Reader stays read-only in Expert; a real user is disabled while an older authorized inventory response is held, and releasing it cannot restore the session/data |
| Independent server deadline | Close the browser; wait the production 120-second window; unchanged deadline and real restored OVS tag; reopening shows server `rolled-back` |
| Responsive responsibilities | Desktop configuration, tablet existing Safe Apply review/confirmation, mobile incident review/rollback without starting a transaction |
| Visual and keyboard | Standard/light, Expert/dark, native advanced values, tablet, 390px mobile, enlarged text, skip link and visible keyboard focus |
| Build and static boundary | Svelte check, production build, embedded deep links, CSP without inline permission, no-store shell / hashed assets, unknown API routes never become HTML |
| Watchdog / decision concurrency | A decision waits within the IPC budget for a watchdog observation; concurrent identical decisions have one durable result; expired sequence requires fresh review; uncertain failures never unlock a replacement request |

`tests/frontend/formal.spec.ts` runs through the two actual daemons and native OVS. Its fixture reuses the kernel system datapath, veth namespace and interface-bound TCP probe from #40. It does not replace API responses with fixture JSON; interceptors only withhold or drop actual authorized responses to exercise delivery failures and concurrent recovery. Synthetic accounts, private test TLS identity, network, services and data are removed after the suite. The deadline case has a separate synthetic principal to respect the production authentication rate limit. Trace/HAR/storageState recording is disabled to prevent credential capture.

The original #40 VM recovery and three-schema native execution matrices remain separate mandatory CI steps on amd64 and arm64. Running the frontend on one actual OVS binary with a schema fixture does not claim certification of multiple OVS binary releases.

Artifacts: `go-runtime-{amd64,arm64}` includes `frontend.xml`, `frontend-evidence/*.png`, `go-frontend.json`, sanitized daemon journal and any test-failure screenshot. Local client tests cover original identity persistence, storage failure before dispatch, receipt uncertainty, account isolation, late responses, advanced values and conservative action/deadline gates.

## Review disposition

The first-slice scope is ready for the delivery gate above. Screenshot inspection covers Standard/light, Expert/dark, tablet, 390px mobile, enlarged text, the three-way Diff, lost-reply recovery, conflict choices and server rollback. Enlarged text originally squeezed the brand mark; relative sizing and adaptive columns correct that issue. Repeated test authentication now uses an independent deadline-scenario principal without changing production rate limits. Real amd64 browser execution also exposed transient watchdog-lock contention during confirmation; bounded waiting, receipt replay under the lock and explicit monotonic version rejection address it, with concurrent-decision regression coverage.

The final CI record must contain all seven formal-browser cases on each native architecture, the complete Go race suite, the retained three-schema field/Safe Apply matrices, kernel management-network recovery, 31 reference-prototype browser cases, unit/contract checks and integration tests. Native crash-child and filesystem helper cases are invoked by their dedicated fixtures; ordinary Go-suite helper skips are not acceptance coverage. Windows does not substitute for Linux runtime acceptance.

#20/#21/#22 and full migration #54 remain open; accepting this slice does not satisfy those broader feature gates. The narrow Svelte `prefer-const` lint exception is necessary because the script-only lint rule cannot see template bindings; Svelte's own strict check still validates props, bindings, types and accessibility. Scrollable table regions intentionally remain keyboard focusable.

## Retained visual samples

These synthetic screenshots come from the seven-passing-case arm64 browser report at `67c297c`, [run 35554731112](https://github.com/sampsonlor/ovs-webui/actions/runs/35554731112/job/106196025332). They document visual review only; that intermediate run's amd64 failure prompted the decision-concurrency fix above. Final cross-architecture acceptance remains the PR/main gate.

[Standard / light](evidence/svelte-ports-vlan-v0.1/ports-standard-light.png) · [Expert / dark](evidence/svelte-ports-vlan-v0.1/ports-expert-dark.png) · [200% text](evidence/svelte-ports-vlan-v0.1/port-large-text.png) · [Diff / Validation](evidence/svelte-ports-vlan-v0.1/validation-diff.png) · [Lost reply](evidence/svelte-ports-vlan-v0.1/lost-reply-unknown.png) · [External conflict](evidence/svelte-ports-vlan-v0.1/candidate-real-conflict.png) · [Mobile rollback](evidence/svelte-ports-vlan-v0.1/safe-apply-mobile.png) · [Server deadline rollback](evidence/svelte-ports-vlan-v0.1/server-deadline-rollback.png).
