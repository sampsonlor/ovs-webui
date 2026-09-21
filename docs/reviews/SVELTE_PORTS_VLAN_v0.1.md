# Formal Svelte Ports/VLAN review

2026-09-21 · #41 · status: implementation under validation. Acceptance requires the final tested PR head and merged main CI; this document does not pre-claim results.

## Required evidence

| Review surface | Evidence and expected result |
| --- | --- |
| Normal authenticated workflow | Real local account → Go-served static SPA → OVS Ports → VLAN Candidate → three-way Diff → Validation → Applied/healthy → explicit Safe Apply confirmation → durable Job and correlated Audit |
| Lost response and refresh | Browser receives no real admission response; original request remains persisted; refresh + GET receipt restores the same transaction; exactly one POST |
| External conflict | A separate real `ovs-vsctl` writer changes the target field; UI blocks validation and requires snapshot-bound keep-current / keep-mine review |
| Provider and manager failures | Actual OVSDB process outage yields stale inventory and read-only fields; stopped manager yields unavailable authorization with no invented values |
| Permission changes and late response | Reader stays read-only in Expert; a real user is disabled while an older authorized inventory response is held, and releasing it cannot restore the session/data |
| Independent server deadline | Close the browser; wait the production 120-second window; unchanged deadline and real restored OVS tag; reopening shows server `rolled-back` |
| Responsive responsibilities | Desktop configuration, tablet existing Safe Apply review/confirmation, mobile incident review/rollback without starting a transaction |
| Visual and keyboard | Standard/light, Expert/dark, native advanced values, tablet, 390px mobile, enlarged text, skip link and visible keyboard focus |
| Build and static boundary | Svelte check, production build, embedded deep links, CSP without inline permission, no-store shell / hashed assets, unknown API routes never become HTML |

`tests/frontend/formal.spec.ts` runs through the two actual daemons and native OVS. Its fixture reuses the kernel system datapath, veth namespace and interface-bound TCP probe from #40. It does not replace API responses with fixture JSON; the two interceptors only withhold or drop an actual authorized response to exercise delivery failures. Synthetic accounts, private test TLS identity, network, services and data are removed after the suite. Trace/HAR/storageState recording is disabled to prevent credential capture.

The original #40 VM recovery and three-schema native execution matrices remain separate mandatory CI steps on amd64 and arm64. Running the frontend on one actual OVS binary with a schema fixture does not claim certification of multiple OVS binary releases.

Artifacts: `go-runtime-{amd64,arm64}` includes `frontend.xml`, `frontend-evidence/*.png`, `go-frontend.json`, sanitized daemon journal and any test-failure screenshot. Local client tests cover original identity persistence, storage failure before dispatch, receipt uncertainty, account isolation, late responses, advanced values and conservative action/deadline gates.

## Review disposition

Pending final CI and screenshot inspection. #20/#21/#22 and full migration #54 remain open; accepting this slice does not satisfy those broader feature gates. The narrow Svelte `prefer-const` lint exception is necessary because the script-only lint rule cannot see template bindings; Svelte's own strict check still validates props, bindings, types and accessibility. Scrollable table regions intentionally remain keyboard focusable.
