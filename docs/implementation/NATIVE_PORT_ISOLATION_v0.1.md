# Native protected Port configuration

This #43 slice implements `port.isolation.set` using the native `Port.protected`
boolean. Under native NORMAL switching, protected ports do not forward to other
protected ports on the same Bridge; unprotected ports can communicate with both
groups. This does not implement a firewall, general PVLAN or flows-based policy.
The legacy `isolation.configure` intent remains unsupported.

## Authority and bounded configuration

The reserved `ovs.port.isolation.write` capability must be assigned explicitly;
builtin roles and older credential ceilings do not acquire it automatically.
Use the root-only `--grant-port-isolation-role ROLE_UUID` /
`--revoke-port-isolation-role ROLE_UUID` offline actions on an existing role.
They update the role revision and audit the policy transition; repeated
provisioning is idempotent. HTTP delegation still requires the caller to hold
every delegated capability and is not a bootstrap route for this reserved grant.
The root-owned `--local-isolation-ports` flag grants immutable Port management
IDs independently of VLAN, topology and STP policy. An empty list grants nothing.
Current configuration read permission is required before provider access and at
Candidate, Validation and execution admission. Root grant changes invalidate
the policy fingerprint, including revoke/regrant. The public read model preserves
withheld, unsupported and stale observations.

The qualified initial scope is one non-local, single-interface system/internal
Port on a system Bridge with an intact local Port. Its connected graph has at
most 32 Ports. Bond/SLB, STP/RSTP, multicast, Mirror references anywhere in the
database, QoS, exporters, external controllers, DPDK/offload, tunnels, advanced
VLAN/QinQ and ambiguous ownership remain unavailable until separately qualified.
These are qualification boundaries, not assertions that native OVS excludes all
such combinations.

The manager captures the original boolean and complete persistent connected
graph privately. Editing keeps the sealed original. Rebase cannot replace it;
a conflicting draft requires discard and a fresh explicit review. The compiler
atomically waits for all persistent images and row sets in Open_vSwitch, Bridge,
Port and Interface, then changes only Port.protected, its private recovery
marker and next_cfg, followed by a durable commit. Every affected managed graph
member and the root are durably reserved against competing field/graph work.
There is no raw command, arbitrary OVSDB mutation or flow input.

Applied requires the exact returned configuration counter, matching after-image
and marker, current identities and a fresh native proof including Interface
health. A lost commit reply never supplies an invented target and observation
never replays the write. Safe Apply confirmation requires independent management
reachability. Compensation derives only from the durable original and matches
the current marker and complete after-image before restoring; external changes
retain protection as conflicts. Protocol configuration and counters do not by
themselves prove measured packet forwarding effectiveness.

## Qualification status

Implementation is under qualification and has not been released or deployed.
Local Candidate/inventory/provider/contract tests, public compatibility checks,
frontend client tests, type checks and the production build have passed.
Linux amd64/arm64 cross-compilation is separate from actual native acceptance.

Required CI includes all existing matrices, new explicit/current credential and
root authority tests, three actual database schemas on both architectures, and
three namespace/veth data Ports. The packet test checks protected peer denial,
unprotected peer reachability and exact compensation, then loses a separate
native reply to check unknown Applied and no mutation replay. Two additional
production HTTPS/IPC/browser cases cover read permissions, Standard/Expert,
light/dark, desktop/tablet/mobile, Candidate/Diff/Validation, confirmation and
exact Safe Apply rollback. The browser also introduces an external protected
field change, keeps the original frozen, withholds validation/rebase and shows
explicit discard/restage guidance without changing the external configuration.
Standard messages explain native isolation and permissions in plain language;
Expert mode retains the diagnostic codes. Freshness and observation time remain
visible in Standard mode as well as Expert. Browser acceptance includes an empty
filtered list and an actual OVSDB outage/restoration: retained values are marked
stale and configuration remains unavailable until fresh data returns.
Passing final results and screenshot
review remain pending.

The failed source runs remain separate from acceptance: 39887393915 failed
formatting; 39887559728 retained a rolled-back draft before the independent
lost-reply case; 39888271098 attempted HTTP delegation of a reserved capability
and was correctly denied. Provisioning now uses the offline root action.
Run 39889415052 retained a native STP source-preflight refusal. Run 39890762111
exposed the missing HTTP isolation deep links, a test wait that could read an old
pre-commit boolean before external drift, and another safe STP witness-source
dispatch refusal. The deep links and ordered observation wait have dedicated
regressions; source diagnostics now use a closed subphase vocabulary. No native
source proof, authentication, timeout or retry requirement has been relaxed.

The next source cf312e3 passed the native and formal isolation steps on both
architectures. Screenshot review found internal codes duplicated in Standard
validation and missing isolation-specific restage guidance. Those presentation
issues are corrected before final acceptance; its results are retained
separately and do not qualify a later source commit.

Run 39896727759 on db635c3 retained an arm64 native STP preflight refusal in
the external-after-image compensation case. The preparation layer had replaced
its private evidence error with the public Problem, so the closed diagnostic
reported only unproven. Preparation now retains the private cause behind an
unchanged public Problem; error text never includes the native cause. This is
diagnostic preservation, not a claim that the refusal's root cause is fixed.

Run 39898083233 on 1b0f5bc passed all 14 jobs; its 13 official archive digests
are retained. The next source 90d74e3 passed both formal browser suites,
including empty/stale isolation views, but OVS 3.3.9 amd64 rejected a native STP
dispatch at the strict process-FD source check. The log does not identify which
FD vanished. Inspection found that this preflight closed its own read-only DB
and control probes between consecutive /proc inspections, allowing the server
to remove accepted FDs asynchronously. These probe connections now remain held
within the same attempt, with a fixed twelve-connection budget and cleanup on
return, cancellation or the existing deadline. The FD enumeration, established
peer relation, source/build/authority fences and two-second deadline are
unchanged; vanished FDs still reject. Dedicated lifecycle/budget regressions
join the existing source admission test. This removes self-induced connection
teardown during an attempt; actual complete source/main qualification is still
required, and the historical failure is not declared diagnosed solely from the
coarse process-FD code. No retry was added.

Run 39902587870 exposed an arm64 three-schema fixture ordering error in the
raw configuration-ABA preflight case: its wait for restored originals could
match the original cached image before either real update arrived. The delayed
transient image then triggered the scope check, or contaminated the following
cancel case before it reached the intended boundary. The fixture now waits for
the transient image and then the restored image, in order, after the two actual
transactions. It retains the same native version-fence error assertion, all
authorization/cancellation assertions, test names and production deadlines.
This is setup synchronization, not a retry of a failed preflight.

This slice does not close #43. Multicast, Tunnel, Mirror, QoS/Queue, all three
exporters and Profile/Drift remain separate required work. STP/RSTP #23 completed
its independently qualified main baseline `phase1-spanning-tree-execution-v0.1`.
#71 remains deferred.
