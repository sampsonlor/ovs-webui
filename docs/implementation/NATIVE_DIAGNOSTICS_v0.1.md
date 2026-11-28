# Native diagnostics

The Link/LACP template reads the current manager-owned OVSDB publication for
one Bridge or Port. It accepts 5, 10 or 15 seconds as the collection deadline
and at most 10 Ports and 64 member Interfaces. Runtime link and LACP evidence
can be unknown independently. A caller without configuration access sees
configured LACP withheld. Admission and collection each resolve the exact
active management identity.

The Active Interface Counter template accepts one Port and samples up to four
of its current member Interfaces. It uses adjacent publications from the
shared OVSDB statistics collector. The result preserves both sample times
and decimal deltas. A missing sample, counter reset or association change
produces explicit gap evidence.

The support archive contains only the chosen Runtime, OVS, System Health,
Events and Audit sections. It uses bounded structured observations and keeps
unavailable sources and truncation visible. Its manifest includes the
redaction policy, each section digest and a 15-minute expiry. The archive
excludes raw provider rows, packet data, process output and supporting secrets.
Every download part checks the owning principal and current permissions for
all included sections.

Packet Capture is opt-in through the root-managed
`CAPTURE_INTERFACE_IDS` runtime setting. Input is up to eight comma-separated
active Interface management IDs. In a systemd deployment, the reviewed
override enabling this provider adds AF_PACKET to the manager address
families AF_UNIX, AF_INET, AF_INET6 and AF_NETLINK. The product supports the ARP, Slow Protocol
and LLDP Ethernet-header profiles. The kernel socket is receive-only from the
product's perspective and is bound to the reviewed Interface and protocol.
The exported PCAP has a 14-byte snap length. A capture cannot continue when
its link association changes or the link event witness is lost. Scope, time,
bytes, and a 4,096-packet ceiling limit each capture. A reached limit or a
kernel drop count is visible in the Job evidence. Pending capture uses the
original Job and cancellation path.

The operator needs a current elevated browser grant to dispatch Packet
Capture. Current diagnostic and inventory permissions remain required while
the capture is running. A cancelled, interrupted or unproven capture drops
its incomplete artifact. Capture previews and download references are
bounded and expire after 15 minutes. Each download verifies bytes against
the reported SHA-256 before saving.

Manager restart retires unfinished native diagnostic Jobs. It never resends
their original requests. Accepted commands retain their original Job,
correlation and audit through the shared receipt and evidence services.
