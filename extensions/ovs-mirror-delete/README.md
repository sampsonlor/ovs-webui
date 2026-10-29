# Optional native Mirror unregister correction

The user approved this route on 2026-10-28. The optional correction and build
admission are implemented. Acceptance requires complete source and independent
main native qualification, recorded separately in the accepted baseline and #43.
This document alone does not establish Mirror acceptance or production deployment.

## Native behavior and scope

The pinned OVS 3.3.9 / 3.7.1 / 4.0.0 unregister path removes Mirror state but
returns without requesting cached-flow revalidation. Rejected source ce31eeb,
run 40628786740, reproduced retained copies in all six actual version/architecture
groups: five cases each, including snaplen 32 and RSPAN VLAN 200. Empty native
Bridge.mirrors and settled configuration counters did not stop old cached actions.

Each version patch adds one REV_RECONFIGURE assignment in that branch, guarded
by ENABLE_OVS_WEBUI_MIRROR_FIX. The new configure option
`--enable-ovs-webui-mirror-fix` defaults to **no**. The default build retains
upstream behavior, and its cached-flow counterexample is tested separately.
The STP read-only extension/ABI/registry and all accepted parent profile files
are unchanged. Combined builds have additional distinct profiles.

`upstream.json` pins the archive, commit, pristine source files, exact reviewed
STP-composed inputs and patch digest. `apply.py VERSION SOURCE ARCHIVE` checks
those bytes before applying; add `--with-witness` only after the separately
pinned STP patch. The correction never requests a lock or changes Mirror
selectors, native GC, database transactions or a production datapath directly.

## Independent runtime admission

Mirror configuration requires independent account capability and exact current
root object/name grants **and** the explicit manager options:

- `--mirror-runtime-socket`: the local actual daemon control socket;
- `--mirror-runtime-executable`: the corresponding root-owned executable;
- `--mirror-runtime-manifest`: a root-owned, non-writable build manifest;
- `--mirror-runtime-manifest-sha256`: the independently reviewed manifest hash.

The manifest format is `ovs-webui.mirror-build.v1`, with exact keys
`format`, `profile_sha256`, `architecture`, `variant`, `executable_sha256`.
The profile digest must name compiled reviewed component bytes; accepted variants
are `fixed`, `witness-enabled`, `witness-sanitized`. A privileged manifest maps
those reviewed source components to the exact executable hash. This is an
explicit root trust decision, not a signature or reproducible-build proof.
There is no public enrollment API, environment discovery or version-string
fallback in the production service.

The manager hashes opened manifest/executable files, verifies root ownership,
mode and stable inode/timestamps, and binds the actual Unix peer process to the
configured executable. Preparation, dispatch and Applied proof also verify the
actual established connection to the exact observed DB peer, file, root and
schema. No control RPC is sent. Checks have a two-second bound and no trust cache.
Each inventory monitor retains one live control transport to avoid creating its
own socket-dump churn; every observation still rehashes both current files and
rechecks the original peer incarnation. A failed observation closes it and
withdraws admission, without reconnecting within that observation.
Dispatch repeats the check after the admission callback, before the sole native
mutation send. Denial logs use fixed internal reason codes through the shared
redaction boundary, with no private paths, configuration values or source IDs.
Configuration counters still do not prove receiver delivery.

Strict process inspection requires the optional process-read capability shown in
`packaging/examples/ovs-mgrd-mirror.conf.example`. The default service omits
`CAP_SYS_PTRACE`; installing an optional drop-in requires deployment review
because this capability permits more than reading OVS. CI opts in only within
its disposable fixture and restores the original unit afterward.

Without qualification, observations remain available and new Mirror writes are
unavailable. The durable admitted plan retains its original build pin for
compensation; removing new admission/root grants does not discard that pin.
Recovery still requires the same actual qualified source, identity and guarded
native after-image. Replacing the binary/manifest or losing source proof blocks
rather than replays recovery.

## Isolated proof

The distro fixture retains five unfixed cached-flow reproductions for each of
three schemas on both architectures. A distinct source MAC exercises a fresh
flow after deletion; the manager must reject new Mirror intents throughout.
The optional fixed 3.3.9 build runs the complete five-case traffic/recovery
matrix with those three explicit schema combinations on both architectures.
Actual pinned 3.3.9/3.7.1/4.0.0 builds run the full matrix in enabled and sanitized
variants; default variants retain the unfixed counterexample. These evidence
kinds are distinct and are never added together as successful recovery cases.

The new five-case actual-build Mirror matrix has a 360-second Go test budget
and a 390-second enclosing process budget, based on measured sanitizer overhead.
Existing native matrices, two-second source/build checks, authentication and
Safe Apply/recovery deadlines are unchanged; all five cases remain required.

The full matrix retains direction/VLAN/snaplen/RSPAN assertions, exact edit
compensation, deletion no-copy, manager reopen/deadline recovery with current
admission removed, fresh identities, CAS, lost replies and no replay. It also
rejects missing pins, wrong digests, wrong executables, unknown profiles and a
manifest made writable inside the final admission callback. No datapath purge,
synthetic mirror flows, unrelated configuration flush or deadline relaxation is
used to manufacture a passing result.

`tests/ovs_mirror/build.sh` builds the standalone 3.3.9 disposable CI runtime.
`tests/ovs_witness/build.sh` composes all three pinned versions; opt-in variants
explicitly enable both independent features. Every prior native/auth/recovery
and browser gate remains required. Actual screenshot review and independent
main qualification remain mandatory. #71 is still deferred.

Sources: [3.3.9](https://github.com/openvswitch/ovs/blob/1a3fefbcd0b0f70ced621b92e286fef3dffaac09/ofproto/ofproto-dpif.c),
[3.7.1](https://github.com/openvswitch/ovs/blob/7921d9c6924b8934ea1de9481891ac1172649280/ofproto/ofproto-dpif.c),
[4.0.0](https://github.com/openvswitch/ovs/blob/b89dc80f0143f5bf3f58c48d54e82cc7f8d29da8/ofproto/ofproto-dpif.c).
