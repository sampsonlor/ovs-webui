# Native protected Port configuration

This #43 slice implements `port.isolation.set` using the native `Port.protected`
boolean. Under native NORMAL switching, protected ports do not forward to other
protected ports on the same Bridge; unprotected ports can communicate with both
groups. This does not implement a firewall, general PVLAN or flows-based policy.
The legacy `isolation.configure` intent remains unsupported.

## Authority and bounded configuration

The reserved `ovs.port.isolation.write` capability must be assigned explicitly;
builtin roles and older credential ceilings do not acquire it automatically.
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
exact Safe Apply rollback. Passing results and screenshot review remain pending.

This slice does not close #43. Multicast, Tunnel, Mirror, QoS/Queue, all three
exporters and Profile/Drift remain separate required work. STP/RSTP #23 also
requires its independently qualified main baseline. #71 remains deferred.
