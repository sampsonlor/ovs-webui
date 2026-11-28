# Independent confirmed VLAN checkpoint

The root maintenance command can publish one closed Manager database's latest
confirmed access VLAN change as an independent recovery file. The accepted
transaction must contain one local standalone system or internal Port. Record
its confirmed transaction ID and instance generation before running maintenance.
Before publishing, stop `ovs-mgrd` through the normal maintenance procedure.
Keep OVS running. Prepare a separate root-owned directory with mode `0700`.

```sh
ovs-mgrd --publish-vlan-checkpoint "$transaction_id" \
  --checkpoint-generation "$generation" --checkpoint-root "$directory" \
  --database "$manager_database"
```

The command reads a closed `manager.db` through a pinned read-only descriptor
under the normal Manager process guard. It requires the exact database schema,
a confirmed Safe Apply and transaction journal, and confirmed inventory with the
same generation, root and OVS schema. Both original object bindings must still
be active. Extra operations, ambiguous topology and unsupported VLAN fields
fail before publication. The command does not connect to OVS or start either
management service.

The `0600` file is named `last-known-good-vlan.json`. Its manifest records the
Manager database ID, schema and recorded software version, exact transaction,
confirmation timestamp and instance generation. The `recovery` member contains
the original Port and Interface bindings with the confirmed access VLAN mode,
tag and empty trunk set using `ovs-webui.port-vlan.v1`. The command excludes
native provider payloads, opaque maps, credentials and keys. A SHA-256 checksum
covers the canonical manifest and recovery payload together. It checks integrity
under the designated trusted root, not the identity of an untrusted sender.

Publication uses a private temporary file, syncs its bytes, and atomically
replaces the previous file. Keep the fixed lock file in the root. Files from
another Manager database and older or conflicting confirmations are rejected.
After uncertain publication, independently verify the recorded transaction and
generation. An interrupted process may leave the single private staging slot
under the lock. The verifier can still validate the previous published file.
The next successful publisher validates and clears that exact slot under the
same lock before writing new bytes.

```sh
ovs-mgrd --verify-vlan-checkpoint "$transaction_id" \
  --checkpoint-generation "$generation" --checkpoint-root "$directory"
```

Verification works with the Manager database offline or absent. The report's
`evidence_scope` is `confirmed-manager-transaction`. The file proves the scoped
historical confirmation under the trusted storage policy. To review possible
recovery, extract its `recovery` member as JSON and use Configuration Transfer.
The existing destination/member review, Candidate, Diff, Validation and Safe
Apply remain mandatory. Verify current inventory and any generation transition
through the normal Manager workflow.

The checkpoint is a separate artifact. Existing v1 sealed backup pairs still
contain both SQLite images and their original manifests. On each later eligible
confirmation, repeat the explicit offline publication if a fresh file is needed.
Automatic publication and other recovery scopes require separate qualification.
