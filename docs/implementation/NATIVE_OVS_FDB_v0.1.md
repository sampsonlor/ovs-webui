# Native OVS FDB observation

FDB observation is disabled by default. Enable it by adding all five options to the existing `ovs-mgrd` invocation.

- `--fdb-observe-bridges` selects up to eight comma-separated, exact Bridge management IDs.
- `--fdb-witness-socket` identifies the absolute local `ovs-vswitchd` control socket.
- `--fdb-witness-executable` identifies the root-owned executable of the qualified optional FDB witness build.
- `--fdb-witness-manifest` identifies that build's root-owned manifest.
- `--fdb-witness-manifest-sha256` supplies the reviewed SHA-256 of the manifest.

Incomplete option sets reject startup. The observation allowlist is independent of configuration write grants. List and detail reads require both `inventory.read` and `state.read`. Standard and Expert use the same authorization.

Source admission checks the FDB-specific build profile, architecture, executable and manifest against the configured daemon. The bounded native command is `ovs-webui/fdb-sample-v1` with the installed Bridge UUID and name. Before and after the sample, the provider verifies the native witness, installed Bridge, schema and daemon/database relationship. Qualification uses the existing `AF_UNIX` control connection, `AF_NETLINK` socket diagnostics, process identity, executable and database-file evidence. Required evidence that cannot be established makes the source unavailable. The existing packaged socket families cover these reads. Build and process qualification are described in the [witness preflight](SPANNING_TREE_WITNESS_PREFLIGHT_v0.1.md) and [source authority](SPANNING_TREE_SOURCE_AUTHORITY_v0.1.md) documents. The current FDB profiles are admitted through [fdbbuild](../../internal/fdbbuild/build.go).

The shared collector schedules rounds every 12 seconds. Each selected Bridge has a two-second observation budget and retains at most 256 native entries. A public page contains at most 50 retained entries. A sample expires after 36 seconds, and a stale OVS association can invalidate it earlier. HTTP filters and refreshes read the shared cache. A new collection round publishes current successes and failures together.

`coverage.bridges_total` counts active managed Bridges in the requested Bridge or Port scope, including managed Bridges outside the observation allowlist. `bridges_sampled` counts qualified current samples. An unfiltered view can therefore report partial coverage when only some managed Bridges are enabled. A scope with no qualified sample is unavailable. A complete zero-row sample and a complete sample with zero filter matches are valid empty results.

`entries_total` is the reported native count across sampled Bridges. `entries_sampled` counts retained entries before HTTP filtering. MAC, VLAN, Bridge and Port filters select from those retained entries. `truncated` reports collector loss independently of `next_cursor`, which continues the retained result. A zero-match result under partial or truncated coverage describes only the observed portion.

Port links identify the logical managed OVS Port when its current association is resolved. Unmapped or ambiguous native ports carry an explicit resolution state. Port-scoped coverage is degraded when unresolved associations prevent a complete selection. Native age is retained in seconds. `last_seen_at` is an estimate calculated from the sample time minus that reported age. Static entries can omit age and the estimate. The page labels the sample time separately.

Sealed cursors bind the credential, permission revision, selection, limit, provider generation and result snapshot. They expire after 30 seconds and return `CURSOR_EXPIRED` when the bound context changes or current evidence becomes unavailable. Source failures and expiry withhold affected rows. A missing detail under incomplete coverage returns a structured 503. A confirmed missing entry under complete coverage returns 404. JSON export contains only the current page and its query, source, sample time, coverage and pagination metadata.

The implementation and bounded wire checks live in [the FDB provider](../../internal/provider/ovsdb/fdb_linux.go) and [its tests](../../internal/provider/ovsdb/fdb_linux_test.go). The [native browser fixture](../../tests/daemon/fdb_frontend.py) and [browser cases](../../tests/frontend/fdb.execution.ts) exercise the formal service and page. Endpoints remains a separate observation domain with its own source and acceptance.
