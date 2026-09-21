# Svelte Ports/VLAN first slice

2026-09-21 · #41. Build and review gate tracked in [the review matrix](../reviews/SVELTE_PORTS_VLAN_v0.1.md).

`frontend/` is the formal Svelte 5 + TypeScript + Vite client. It retains the accepted infrastructure palette, five product domains, Bridge → Port → Interface relationships, Standard/Expert depth, and shared Candidate / Validation / Transaction / Job / Event / Audit resources. The existing React/Vinext prototype and Node lab remain independent reference tools.

## Build and serve

```sh
pnpm install --frozen-lockfile --no-runtime
pnpm frontend:check
pnpm frontend:build
go build -trimpath -o outputs/runtime/ovs-webd ./cmd/ovs-webd
go build -trimpath -o outputs/runtime/ovs-mgrd ./cmd/ovs-mgrd
```

`pnpm build` builds both the formal SPA and the reference prototype. Vite writes hashed assets into ignored `internal/web/assets/spa/`; Go embeds them when compiling webd. Linux service startup, explicit database/account/key initialization, HTTPS and the root-configured Safe Apply probe still follow [the runtime](GO_RUNTIME_IPC_v0.1.md), [authentication](AUTH_GRANTS_v0.1.md) and [Safe Apply](SAFE_APPLY_v0.1.md) instructions. There is no Node or Python process in production.

Browse the configured webd HTTPS origin. `/ports`, `/ports/{management_id}`, `/ports/{management_id}/vlan`, `/changes/candidates/{id}`, `/changes/validations/{id}`, `/changes/transactions/{id}` and `/operations/{jobs,events,audit}/{id}` survive navigation and refresh. `/changes/candidate` resolves to the current server-owned Candidate; an older ID does not silently become its successor. Missing built assets return 503 rather than an apparent working product. Unknown API routes stay JSON errors and never fall through to HTML.

The SPA has no inline scripts/styles, third-party scripts, CDN fonts or `unsafe-*` CSP permissions. The shell is `no-store`; hashed scripts/styles are immutable. Cookies stay Secure/HttpOnly and same-origin. Password fields are cleared on submission; CSRF and session claims exist only in memory. Local storage contains theme/depth preferences and, while necessary, the principal-bound original request ID/domain/epoch/path. It never contains credentials, configuration form bodies or session secrets.

## Authority and recovery

The client uses the generated public v1 DTOs and existing REST services. Every write carries the service request epoch and a UUIDv7 idempotency key. Candidate writes carry their strong revision. A pending identity is saved before dispatch; a storage failure prevents dispatch. After an unacknowledged mutation, the UI blocks new commands and queries the original receipt. Missing, forbidden, mismatched and unknown receipt states do not prove non-execution. Recognized accepted/completed receipts restore their linked resource without another POST.

Known draft-only CAS/semantic rejections expose `command_effect: not-started`; this narrow mapping covers errors that occur before the Candidate write commits. Validation and execution handoff failures remain unknown. Do not generalize an HTTP status to a non-execution guarantee.

Inventory VLAN edit hints use the same immutable observation and root-owned local-Port policy projection as validation. They require a fresh, known, supported field group, local authority and current capabilities. Externally controlled, missing, stale and withheld values stay read-only. Only the four reviewed Standard modes are editable in this slice; native defaults, QinQ/CVLANs, reserved values and future modes are displayed without normalization. Backend validation and admission still independently enforce every condition.

A refresh first verifies the session, then reads authorized resources. Response fences prevent an old route/account/permission batch from restoring data after navigation or logout. Authorization failures clear protected page values. Server `knowledge`, commit, Applied, Health, Safe Apply state, sequence and allowed actions remain separate. The countdown is anchored to server time and elapsed monotonic browser time; it never creates a terminal state. Desktop prepares new changes; tablet reviews and handles existing Safe Apply; mobile inspects incidents and can request rollback of an existing transaction.

## Scope limits

This is the first formal Ports/VLAN slice, not full Phase 1 or the complete UI migration. #20 (full user/role administration), #21 (complete Interfaces), #22 (cross-Port VLAN workflow), #54 (remaining Svelte pages and bilingual coverage) remain separate. Visibility and full administration screens explicitly state their remaining scope. Evidence lists disclose truncation; full API paging/export stays available. `frontend:dev` is an asset-development server, not a replacement authentication or switch-management service.

Svelte is mounted with its documented [client component API](https://svelte.dev/docs/svelte/imperative-component-api), with [TypeScript](https://svelte.dev/docs/svelte/typescript) and the official [Vite plugin](https://github.com/sveltejs/vite-plugin-svelte). Production dependencies are pinned in `pnpm-lock.yaml`.
