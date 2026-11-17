# Native QoS detachment

A desktop operator can stage detachment of a dedicated linear HFSC QoS from
its existing standalone Linux veth Port. The change clears the exact
`Port.qos` reference. QoS and Queue rows retain their identifiers, settings and
Queue numbers. Forwarding can continue without the shaping and DSCP marking
from that binding once Linux returns to its built-in `noqueue` scheduler.

Provision the operator role with `--grant-qos-role=ROLE_ID` and authorize the
exact pair with `--local-qos-detach-bindings=PORT_MANAGEMENT_ID:QOS_MANAGEMENT_ID`.
The detachment grant declares exclusive management of that egress scheduler.
Rate editing has its own pair grant. Standard and Expert use the same
authorization and Safe Apply checks.

Candidate captures the current Port, Bridge, Interface, QoS and all mapped
Queues. The native transaction checks the captured dependency graph and
incoming references before clearing the binding. Confirmation requires a
fresh OVSDB observation, daemon acknowledgement, the same veth with classless
`noqueue` and a reachable management path.

Safe Apply restores the same binding if the native rows, reference ownership
and Linux state still match the expected state. It checks that the original
HFSC configuration is installed before reporting rollback complete. If the
daemon is paused, expiry can restore the database binding while Linux still
has the exact original configuration. A foreign qdisc, shared binding or
changed identity stops automatic execution or recovery.

## Bind an existing QoS

Desktop operators can also attach an existing dedicated linear HFSC QoS to
a standalone Linux veth Port whose original scheduler is classless
`noqueue`. Provision the role with `--grant-qos-role=ROLE_ID` and the
exact pair with `--local-qos-attach-bindings=PORT_MANAGEMENT_ID:QOS_MANAGEMENT_ID`.
Each grant pairs one Port with one QoS. Candidate shows the proposed Port
binding and captured rates. The transaction changes the Port reference and
preserves the existing QoS and Queue rows, including DSCP settings.

Dispatch checks for an empty Port reference, exclusive QoS and Queue ownership,
and the original Linux scheduler. Safe Apply opens confirmation after daemon
acknowledgement, exact installed HFSC rates and a healthy management path.
Rollback clears the same binding and verifies the original `noqueue` scheduler
on the same link. A foreign or partially installed scheduler can prevent guarded
rollback when ownership cannot be proven.
