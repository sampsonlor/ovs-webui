# Auth Grant and per-operation authority v0.1

Issue #34 implements the local authentication and authorization foundation for
#20 Users/Roles, #27 AAA and #28 API Tokens. Those functional issues retain their
own service, page and exception acceptance gates. All #20–#28 belong to the
Phase 1 milestone. Public API #33 was accepted as PR #59, merge `9f641dde`, tag
`phase1-public-api-v1.0`.

## Authority and delivery boundary

`ovs-mgrd` owns principal/password verifiers, role templates, grant ceilings,
token scopes, expiry, elevation, revocation and security audit in `manager.db`.
`ovs-webd` maps browser cookies to encrypted grants in `web.db`. Every security
operation is selected from the compiled REST registry and independently
validated by mgrd. IPC callers cannot supply actor, role, priority, SQL or a
provider command. A preliminary capability check is advisory; execution checks
the actual operation, resource, current permissions and risk again.

The implemented operations are Local authenticate, session inspection/logout,
reauthentication, users/roles, scoped token creation/revocation and their bounded
reads/receipt/job recovery. TACACS+ remains explicitly `AUTH_PROVIDER_UNAVAILABLE`
until #27 and #35 provide and accept its provider, secret store and chain. Other
domain operations remain unavailable until their respective service gates;
grant validity does not authorize arbitrary OVSDB writes. Configuration readiness
remains false. Full Svelte account and administration pages are tracked by #41
and #54 and the existing functional issues.

## Credentials and initial budgets

- Local password verifiers use versioned Argon2id, 64 MiB, 3 iterations,
  parallelism 1, random 16-byte salt and 32-byte output. Only this bounded version
  is accepted; unknown cost parameters cannot request arbitrary allocations.
  New passwords require 12–1024 bytes. See the [Go Argon2
  implementation](https://pkg.go.dev/golang.org/x/crypto/argon2).
- Two active password derivations, no unbounded queue; ten attempts per username
  and sixty attempts globally in sixty-second attempt windows. Attempt accounting is
  persistent and hashed. Reauthentication shares the same limits.
- Grants, cookies, CSRF values and API tokens have independent prefixes and
  256-bit random secrets. Manager stores only credential hashes. Grants expire
  after eight hours or fifteen idle minutes; elevation lasts at most five
  minutes and cannot extend the session. Normal restart preserves the deadlines.
- API tokens have explicit scope and expiry, up to 365 days. Scope is limited by
  the creator's current effective permissions; later reductions apply on every
  use. A token cannot be used as a browser grant or reauthenticate as a password
  session. Token plaintext is returned only on first creation, never in stored
  receipts or replay. Repeated request IDs recover the same receipt.
- Up to 1,000 local principals, 128 roles, 4,096 active grants and 4,096 token
  records. Security audit has a 100,000-record admission limit pending the shared
  retention service #37; exhausted evidence capacity fails closed.

## Browser and persistence

The cookie is `__Host-ovs_session`, Secure, HttpOnly, SameSite=Strict, Path=/ and
has no Domain. Login requires the canonical HTTPS Origin. Cookie mutations also
require the session-bound `X-OVS-CSRF-Token`; Bearer credentials and cookies cannot
be combined. Only `ovst_` tokens are accepted in public Authorization headers;
manager grants are restricted to the credential-checked Unix IPC.

Migration 004 adds typed security metadata and audit in manager.db and sealed
browser mappings in web.db without replacing the accepted migrations. Session
envelopes use AES-256-GCM with random nonces. Associated data binds the consumer,
envelope version, database identity and cookie hash. Swapping a row, changing a
principal, extending an unencrypted expiry or restoring a logged-out web mapping
cannot create manager authority. Current claims are always obtained from mgrd.

The session key and manager authentication key are separate 32-byte files in
their respective private state directories. They are created only by explicit
local commands, opened without following links, and require the service owner,
private permissions and a single hard link. Normal startup never regenerates
a missing key. This consumer-specific envelope foundation is not completion of
#35's full SecretStore, TLS activation, privileged partitions or key rotation.

## Admission, permissions and recovery

The five role templates contain explicit canonical capabilities. Public v1
transport aliases are mapped to the accepted design's canonical namespace;
there is no role-name bypass. SecurityAdmin does not imply network writes, and
NetworkAdmin does not imply security administration. Unknown capabilities are
rejected. Existing grants cannot gain permissions beyond their issuance ceiling.
Self-service tokens use `access.tokens.self`; managing another principal's token
also requires `access.tokens.manage`. Token Bearer access is constrained by these
same explicit scopes, including reads of its owner's token metadata.

The mgrd systemd limit is 256 MiB with a 192 MiB Go memory target, accounting for
two 64 MiB password workers and bounded schema/SQLite/runtime overhead. Native
service smoke tests record actual peak memory and concurrent login timings.
The Go race-test package timeout is 180 seconds because full-cost Argon2id tests
on arm64 exceed the earlier 90-second cumulative limit; production request
deadlines remain five seconds.

Every security mutation requires current elevation. A shared mgrd decision gate
orders revocation, policy changes and execution. Password derivation happens
outside that gate and the database writer; authorization is repeated afterwards.
The security effect, terminal Job, receipt and redacted audit commit atomically.
Revocation that wins before dispatch prevents the operation; a completed effect
retains its evidence. The final usable local administrator cannot be disabled,
deleted or stripped of its administrator capabilities, including indirectly by a
role change. Deletion retains a disabled identity tombstone for durable evidence.

User and role changes use strong If-Match. Request IDs/epochs and request HMACs
reuse #33's persistent receipt repository. Receipt and job reads require the
current owner and original-operation permissions. List cursors are bound to the
principal, permission revision, operation, filter, limit and a thirty-second
snapshot lifetime. Responses are bounded below the IPC response limit.

Clock rollback past the persisted authentication watermark refuses authorization.
Loss or corruption of manager.db does not create a new principal database.
Automatic restoration/credential-epoch rotation remains subject to #35/#48's
explicit restore workflow; a plain file copy is not an authorized restore.

## Local initialization

Run the existing explicit database initialization first. The following commands
are installation actions, not HTTP endpoints. Password input must be supplied
through stdin, never a command-line argument, environment variable or log.

```sh
# root, after creating the private manager database
ovs-mgrd --init-auth-key
ovs-mgrd --bootstrap-admin admin < /secure/one-time-password-input

# ovs-webui-web, after creating the private web database
ovs-webd --init-session-key
```

For custom database locations, pass `--database` on each initialization command.
Keys default to `auth.key` and `session.key` beside the respective database;
`--auth-key-file` and `--session-key-file` select explicit private files.
Configure `--public-origin=https://host[:port]` on webd before enabling browser
authentication. There is no default password, HTTP bootstrap, anonymous admin or
automatic key replacement.

## Validation

The completed technical matrix and retained execution evidence are in
[AUTH_GRANTS_v0.1](../reviews/AUTH_GRANTS_v0.1.md). Native Linux amd64/arm64 each
passed 76 top-level Go race cases plus real systemd/Unix peer/HTTPS login,
concurrent password workloads, revocation and independent process recovery.
The existing 208 regression, 3 integration and 26 browser tests, contract
compatibility, type/lint checks and production build passed. User acceptance is
still pending; this does not close the complete functional issues.
