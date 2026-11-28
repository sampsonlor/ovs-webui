# Management backup and restore evidence

The root maintenance command `ovs-mgrd --create-offline-backup-pair`
accepts an existing private destination through `--backup-pair-root`.
Supply the installed web service UID and GID with `--webd-uid` and
`--webd-gid`. The database paths default to the packaged management state
directories. The command requires the current schema of both installed
daemons and refuses live database writers.

The manager owns its database. It launches the root-owned registered
`/usr/libexec/ovs-webd` executable under the web service's UID and GID.
That process owns the web database throughout its snapshot. The coordinator
validates the two independent SQLite images and publishes the private pair
after both component streams close successfully. A post-rename sync failure
reports the original pair ID with an uncertain publication result.

The private pair contains the two SQLite images, their individual manifests,
and the pair manifest. The manifests bind the database identities, software
versions, schema versions, sizes and SHA-256 digests. Existing ciphertext,
checkpoints and generation history are part of the images. The owner's key
files stay in their separate private storage.

`ovs-mgrd --verify-offline-backup-pair <id> --backup-pair-root <directory>`
verifies the original pair and reports key version metadata from
`secret_records` by partition.

## Sealed restore staging

Use a separate, existing root-owned directory with mode `0700` for staged
restore copies. Both roots must be absolute and must have a trusted ancestor
chain. The staging command reads the sealed backup pair and creates a new stage
ID. It takes no database, service or key arguments.

```sh
ovs-mgrd --stage-offline-restore-pair "$backup_id" \
  --backup-pair-root "$backup_root" --restore-stage-root "$stage_root"

ovs-mgrd --verify-offline-restore-stage "$stage_id" \
  --restore-stage-root "$stage_root"
```

The pair's two images are rechecked for SHA-256, SQLite integrity, database
identity and schema. They are published together in `restore-stage-<id>`.
The verification command reads only that stage, even if the original pair
is offline. After an uncertain publication the command returns the stage ID
with a failing exit status. Verify that ID before considering another attempt.

`sealed` is the durable checkpoint before owner preparation. Manager and Web
preparation and OVS reconciliation remain required. Both slots are root-owned
and private. The Web service owner cannot traverse the staging tree and needs
a separate private working copy in an approved owner-accessible directory.

The manifest's key evidence covers only version metadata in `secret_records`.
`no-secret-records` describes that table partition. The Web database can also
carry ciphertext in `tls_local_state` and `browser_sessions`. External Manager
auth material, the Web session key ring, TLS keys, legacy formats and external
key providers all require their own owner checks. The manifest includes no
private keys and records no decryption result.

## Requirements for owner preparation and installation

1. Bind each private working copy to the exact sealed stage ID, kind, database
   ID, schema and original digest. Resolve the Web UID and GID and executable
   from the installed services. Check that each intended owner can traverse
   its trusted parent chain and exclusively access its own `0700` directory
   and `0600` image. Keep the sealed stage available for retries.
2. Give each owner independent, approved access to its key material. Validate
   expected version coverage and actual authenticated decryption of the
   retained data. Both owner preparation commands now authenticate the retained
   SecretStore envelopes and require the specific key versions recorded for
   them. Web also requires its managed TLS ring when the archive includes
   managed TLS state, verifies the active certificate identity and clears
   restored browser contexts. Treat externally supplied keys and owner
   actions without a verified result as pending.
3. Run the existing `--prepare-restore-security` action only against those
   owner-specific working copies. Preparation requires the installed schema
   version. Review and perform any required schema migration separately on
   private copies. Discard a working copy if any preparation fails or the
   outcome is unknown. Check and record the committed outcomes
   against the working copy identity and post-prepare digest. Manager must
   revoke grants and tokens, change its auth and API epochs and require OVS
   reconciliation. Web must clear the restored browser contexts and discard
   any TLS trial. Record owner results in a separate durable checkpoint under
   trusted root control. SQLite rows or caller-provided success flags alone
   cannot establish which owner performed the action.
4. Hold the canonical database process guards across both replacements and
   keep the management daemons stopped. A future installer needs a durable
   journal with the before-images, prepared identities and recovery phase for
   each owner slot. Separate stores may span filesystems. Recover interrupted
   replacements before starting either daemon. Verify post-install security
   and bind `--reconcile-ovsdb` to separately reviewed current OVS identity
   evidence before enabling managed writes.

## Prepare a fresh offline destination

Supply a separate, existing root-owned destination parent that the installed
Web service account can traverse. A root-owned directory with mode `0710` and
the Web service group provides this access. Prepare from an exact sealed stage
with the reviewed installed daemon digests and keys available to their owners.

```sh
ovs-mgrd --prepare-fresh-restore-stage "$stage_id" \
  --restore-stage-root "$stage_root" --restore-destination-root "$destination_root" \
  --restore-manager-sha256 "$manager_sha256" --restore-web-sha256 "$web_sha256" \
  --restore-manager-auth-key "$manager_auth_key" --restore-web-session-keys "$web_session_keys" \
  --restore-web-tls-keys "$web_tls_keys" --restore-public-origin "$public_origin"
```

Use `--restore-web-session-key` instead of `--restore-web-session-keys` for a
legacy session key file. Use `--restore-manager-secret-keys` when the restored
Manager store retains encrypted records. The command requires the installed
schema and a settled native configuration checkpoint. It verifies the two
owners' prepared working copies, checks a retained managed certificate, and
publishes `restore-ready-<stage-id>` once without replacing any path. The
manager and web directories have mode `0700` and retain their separate owners.
The root-only manifest records database and executable digests, never keys.

The result is `offline-prepared` and still requires reviewed OVS reconciliation.
Installing over existing state, selecting a new runtime location, and starting
services remain separate operator actions. A failed or uncertain owner step
never publishes working data. A post-publish durability failure returns the
stage ID with an uncertain result. Verify the exact published destination using
the command below. An abruptly interrupted coordinator can leave an unpublished
`.preparing-*` directory under the dedicated root for offline cleanup.

### Verify publication after an interrupted report

Use the sealed stage ID and the two build digests recorded before preparation.
The verifier opens the existing closed destination and its sealed stage under
their trusted roots, acquires both owner process locks, and checks the original
backup pair, database identities, exact schemas, rotated epochs, revoked
credentials and prepared image digests.

```sh
ovs-mgrd --verify-fresh-restore-stage "$stage_id" \
  --restore-stage-root "$stage_root" --restore-destination-root "$destination_root" \
  --restore-manager-sha256 "$manager_sha256" --restore-web-sha256 "$web_sha256"
```

Exit zero returns the original `offline-prepared` report. A missing destination,
busy owner lock, damaged image, or conflicting digest yields a nonzero exit.
Verification reads the sealed and published images through pinned descriptors,
using only the recorded build digests. The report still requires separately
reviewed OVS reconciliation.

The hosted acceptance fixture uses synthetic data and separate service
accounts. It verifies exclusive writer refusal, per-owner acquisition,
complete pair publication, digest verification, a sealed restore independently
verified before and after per-owner preparation, absent-key refusal, auth and
request epoch changes on the working copies, independent verification of
published copies including damage and competing owner locks, and continued
isolated OVS forwarding. A deliberately failed output write verifies recovery
of the committed report from the exact prepared pair. The fixture copies the
two verified published slots under their separate owners. Its Manager
observes the current OVSDB and
requires the exact reviewed digest before assigning a new generation. Both
daemons then start from those same working copies. The HTTPS service presents
the restored managed certificate, rejects the old browser cookie and accepts
a fresh login. The fixture re-verifies the published pair and sealed stage
after shutdown. The original databases and OVS configuration stay in place.

## Transfer managed Port VLAN settings

Configuration Transfer exports selected standalone system and internal Ports
to a deterministic `ovs-webui.port-vlan.v1` document. The document records the
source generation, schema digest, exact Port and sole member, and the explicit
VLAN mode, tag and trunk set. The supported modes are `access`, `trunk`,
`native-tagged` and `native-untagged`. The tag is `[]` for absence or `[id]` for a
value. An empty trunk set retains its native OVS meaning for the selected mode.

Merge preserves fields omitted from a document. Exact Restore requires all three
fields for every source and a reviewed current destination Port and member ID
for each mapping, including within the same generation. A changed member,
different Port shape, native default mode, or unrepresentable before-image
blocks Exact Restore. Customer VLAN tags, QinQ, bridge/local ports, bonds,
topology, host settings and secrets are outside this transfer scope.

Both modes stage the same field-level intents in the current Candidate. Review
the Diff and run Validation before using the normal authorized apply flow. The
export, mapping review, staging and Validation leave live OVS configuration in
place. The isolated Exact Restore test then starts the reviewed Safe Apply,
observes the native VLAN change and requests rollback. It checks the original
transaction evidence after reload and verifies restoration of the exact Port
row and member identity.
