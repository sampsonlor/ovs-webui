# Native QoS detachment

A desktop operator can stage detachment of a dedicated linear HFSC QoS from
its existing standalone Linux veth Port. The change clears the exact
`Port.qos` reference. QoS and Queue rows retain their identifiers, settings and
Queue numbers. Forwarding can continue without the configured shaping once
Linux returns to its built-in `noqueue` scheduler.

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
