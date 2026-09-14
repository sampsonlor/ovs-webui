# Auth Grant review v0.1

Status: implementation in progress; not an accepted baseline.

Issue #34 follows the accepted public API baseline `phase1-public-api-v1.0`.
This review covers authentication and authorization services, not the complete
#20/#27/#28 Svelte pages or later provider implementations.

| Requirement | Evidence to retain |
| --- | --- |
| Local authenticate and bootstrap | Explicit one-time root CLI, Argon2id verifier, no default principal/password |
| Per-operation authority | Compiled operation and current capability/resource checks; no client actor/role authority |
| Revocation/dispatch ordering | Concurrent credential revoke and durable security mutation; winner and receipt agree |
| Last local administrator | Disable, delete, role removal and role-template modification are rejected atomically |
| Session security | Origin/login CSRF, secure cookie attributes, same-origin mutation CSRF, duplicate credential rejection |
| Session persistence | Encrypted grant mapping, restart deadlines, row/cookie/key substitution and stale web DB restore rejection |
| Scope and privilege | Grant ceiling, immediate role reduction, scoped tokens, separate NetworkAdmin/SecurityAdmin |
| Token recovery | One-time plaintext response, no secret receipt replay, expiry/revoke and current-authorization checks |
| Failure/recovery | Idle/absolute/elevation expiry, clock rollback, missing key/manager, storage failures |
| Evidence/resources | SQL-atomic effect/Job/receipt/audit, bounded pagination and password/attempt budgets |
| Transport boundary | Actual Linux Unix peers and HTTPS with independent daemon restart |
| Architecture matrix | Native amd64 and arm64 tests, race detector and password calibration |
| Prototype regression | Existing UI/browser/contract/build checks; no new page acceptance implied |

Standard/Expert remain presentation choices. A submitted mode/role/actor field
cannot grant permission. Desktop/tablet/mobile page responsibilities remain
those of the accepted UI baseline; this service batch adds no new mobile
configuration surface. AAA without its accepted provider reports unavailable;
switching operations remain subject to the later Candidate/Validation/Safe Apply
gates and cannot be directly saved through these security endpoints.
