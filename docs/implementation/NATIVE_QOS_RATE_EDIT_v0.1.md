# Native QoS rate editing

The QoS detail page stages changes to the maximum rate and the existing
Queues’ minimum and maximum rates. The first supported layouts are software
HTB and linear HFSC on one standalone system Port with dedicated QoS and Queue
rows. This operation requires explicit rates on every mapped Queue. Rates use
bit/s in multiples of eight. HTB minimum rates must also satisfy the observed
Interface MTU. Existing bindings, Queue numbers, scheduler type, DSCP and
opaque OVSDB options are preserved.

The root-owned manager configuration grants a reviewed
`--local-qos-bindings=PORT_MANAGEMENT_ID:QOS_MANAGEMENT_ID` pair. This opt-in
assumes exclusive management of the egress scheduler on that device.
Separately provision `ovs.qos.write` with the offline root action
`--grant-qos-role=ROLE_ID`. `--revoke-qos-role=ROLE_ID` removes it.
Users sign in again to obtain an updated credential ceiling. Standard and
Expert share the same permissions. Tablet and mobile retain review.

Candidate captures the current rates and native dependency identities.
An OVSDB transaction checks those dependencies and incoming references before
updating the rate keys. Safe Apply uses independent Linux link, scheduler,
class and filter observations. Confirmation requires an exact installed rate
match and the usual management-path evidence. Rollback checks the native
after-image and verifies restoration of the original installed profile.
If live evidence is missing or conflicts with the captured change, the
transaction can require operator recovery.

Creation, detachment, scheduler conversion, shared rows and advanced settings
remain outside this rate editing operation. The installed rate and Queue
counts do not measure throughput.
