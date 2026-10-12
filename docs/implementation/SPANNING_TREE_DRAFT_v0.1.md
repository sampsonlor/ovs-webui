# STP / RSTP M2b: independently authorized draft review

This slice follows the accepted [M1 observation](SPANNING_TREE_OBSERVATION_v0.1.md) and [M2a basic validator](SPANNING_TREE_VALIDATOR_v0.1.md). It adds preparation on a verified Bridge, a sealed Candidate original/desired image, native field Diff and durable Validation evidence. It does not accept STP/RSTP Basic Manage or complete #23 / #43. The #71 investigation remains Open / Pending.

## Product boundary

Desktop users with current workspace and preparation permission may prepare a single Bridge draft. Tablet and mobile retain observation and draft review. Standard and Expert have identical rules, permissions and blockers; Expert adds technical identifiers. Every preparation goes through Candidate → Diff / Validation. This release has no STP/RSTP executor, direct live save, installed timer proof, forwarding/convergence claim or recovery acceptance.

`bridge.spanning-tree.prepare` is a new, explicitly draft-only operation. The existing released `spanning_tree.configure` arm remains unsupported and unchanged. Public API 1.27.0 adds the optional preparation hint and sealed draft observation; it removes no endpoint or existing request shape. Bridge observation remains Observe with `editable=false`, an empty `allowed_operations` list and its existing pending write reason. A separate `preparation.available` hint is not write authority.

All validations of this operation contain the unconditional blocking check `SPANNING_TREE_EXECUTION_PENDING`. Even a semantically valid draft with a running Safe Apply service has `usable=false` and `execution_ready=false`. A succeeded Validation Job has `applied_outcome=not-applicable`; its ChangeSet and Audit prove review only.

## Independent authorization and current scope

The manager accepts a root-owned `--local-spanning-tree-bridges=<management-id,...>` grant, limited to 128 valid immutable Bridge management IDs. Name, observed external metadata, schema mutability and existing topology/VLAN/Bond/Interface grants do not substitute it. A recreated same-name Bridge has a new identity and no inherited grant.

The current credential must have `workspace.write` and the dedicated `ovs.bridge.spanning-tree.prepare` capability. The latter is included in the builtin Administrator and NetworkAdmin templates. Existing credentials retain their original capability ceiling. Manager preparation rechecks current claims inside its serialized store transaction before signing. Configuration withholding also withholds preparation authority; an observer cannot infer a hidden grant or parameter validity.

Preparation is deliberately restricted to:

- One current system/default-system Bridge rooted in one observed Open_vSwitch, with complete fresh native configuration and compatible mutable basic columns.
- One verified local internal Port and at most 128 Ports. Each Port has one uniquely attached Interface, of system/default-system/internal type, with empty options.
- Known ordinary VLAN membership and no explicit per-Port STP/RSTP overrides, QoS, Bond/LACP configuration, advanced spanning-tree Bridge keys or other native-domain references.
- No external controller and no observed Mirror reference on any Bridge. Mirror output relations are not monitored, so preparation stays closed instead of inferring absence. No unverified offload or external ownership marker on the root, Bridge, Port or Interface is accepted.

Missing configuration, unknown type/authority/reference values, stale observations, retired bindings, unsupported schema and provider degradation stay unavailable. The complete persistent root/Bridge/Port/Interface images are privately hashed with immutable identities. Unknown/unrelated persistent settings remain in the guard; approved target fields have a separate exact conflict comparison. Runtime status is excluded. Private images and arbitrary metadata are not sent to the browser.

These qualifications define this preparation slice. They do not qualify native dispatch or compensation. Bond, patch/tunnel/DPDK graphs, QinQ, controller/Mirror combinations, advanced Bridge/Port parameters and other graph types require subsequent scope review.

## Closed request and captured original

The request supplies an existing immutable Bridge binding and `spanning_tree: {protocol, parameters}`. Protocol is `disabled`, `stp` or `rstp`. Parameters accept only the seven approved native basic keys; a request may edit only the selected active protocol's keys. RSTP hello time has no editable native key. Client originals, dependency images, authority, raw OVSDB rows and unrelated operation fields are rejected.

| Parameter representation | Meaning |
| --- | --- |
| Missing key | Preserve the captured original value, including on restage. |
| Decimal string | Propose that exact raw value; range, timer relations and native caveats are assessed in Validation. |
| Explicit null | Remove that active protocol key; preserve native unset rather than writing a default. |
| Disabled with empty parameter map | Disable both protocol flags in the proposed image and preserve all seven original parameters. |

The manager independently captures both original protocol flags and all seven original parameters, keeping absent keys null. It clones before/after maps separately and seals the complete envelope. Inactive settings are retained and assessed by the M2a validator; editing one protocol does not repair invalid inactive values. No automatic rounding, clamping, protocol choice or default materialization occurs. The editor's blank inputs explicitly request null for active keys.

One spanning-tree intent may occupy the Candidate. Mixed operations, a different target or a different intent ID cannot replace it. Restaging retains the original binding, schema, dependency and complete before image. Diff contains nine fields: two enable flags and seven parameters. Before, Current and After distinguish preserved native unset; conflicted unavailable current values require fresh review.

## Drift and revocation

Read, restage and Validation recheck the current immutable binding, instance generation, schema, local authority, graph and exact original fields. Changes require reconciliation. Rebase is explicitly rejected for this operation: it cannot turn a changed native value into a new original. The user discards the intent, reviews the current Bridge and prepares a fresh draft. Revoked credentials/capabilities and root policy changes invalidate review eligibility without widening the old credential's ceiling.

The preparation capability is not an execution capability. No execution adapter, dispatch authority, timer readback or safety recovery behavior is changed. STP explicit hello-time unit caveats and RSTP timer setter ordering remain exactly the pending execution questions documented in M2a.

## Verification and next gate

Go tests cover independent grants, unknown configuration/reference/type guards, private dependency and runtime separation, null/inactive/raw preservation, sealed originals, drift/restage/Rebase, current credential ceilings and blocked admission with Safe Apply enabled. A real disposable HTTPS/Unix IPC/OVSDB fixture checks draft behavior, native configuration unchanged by stage/validate, durable Job/Audit evidence, parameter failures, drift, revoked root grants and same-name replacement across three schema fixtures on both native architectures. Each report retains its actual daemon binary version separately from the schema label.

Formal browser evidence includes the desktop editor in Standard/Expert and light/dark, tablet/mobile review, the nine-field Candidate Diff, blocked Validation, invalid RSTP priority and drift requiring discard. Existing M1/M2a exception and responsive evidence, native writes/compensation, authentication diagnostics and recovery gates remain required. Acceptance receipts are retained in the delivery PR and annotated `phase1-spanning-tree-draft-v0.1` tag after both feature and merged main CI and visual review pass.

The next M2 gate is guarded native execution with independently sourced installed protocol/priority/timer proof, bounded forward/rollback ordering, compensation from the sealed originals, graph reservations and current authority at dispatch, plus real daemon/restart/crash, OutcomeUnknown, provider degradation and conflict acceptance. Until that gate passes, Basic Manage and the parent issues remain open.
