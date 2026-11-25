# Linux ARP / NDP observation

The Neighbors page reads IPv4 ARP and IPv6 NDP entries from the network namespace of `ovs-mgrd`. The manager collects every five seconds and serves the same RAM sample to authorized callers. Each round reads the kernel link table before and after the two neighbor dumps. A row is joined to an active managed Interface only when the Linux name and ifindex agree at both checks and the OVS binding remains current. The join also supplies proven Port and Bridge references. Ambiguous associations reduce coverage.

The timestamp records the manager's observation. Linux NUD state describes the cached entry. The observations do not establish when a remote device was last seen or forwarding through OVS. Incomplete, Failed and unknown states suppress any retained link-layer address. Proxy entries and unsupported address shapes are excluded. No host neighbor table is written or persisted.

Each collection has a two-second, 4 MiB and 8192-message budget across the link and neighbor dumps. An interrupted or unproven dump clears the previous result. A completed dump can retain up to 2048 managed rows and reports when that retention limit was reached. A change in the OVS association or a source outage fences in-flight samples. Samples older than 15 seconds withhold rows, as do stale OVS associations. A complete empty result requires at least one covered Interface in the requested scope.

Both `inventory.read` and `state.read` are required for list and detail. Filters cover Interface, Port, Bridge, IP family and text. Pages return at most 50 rows or 40 KiB of item JSON. The cursor is sealed to the caller, permission revision, selection and result version. The export contains the displayed page, its query, source, coverage and continuation information. Expert mode adds source detail under the same permissions.

This batch contributes to #25 and #44. Optional LLDP and event-driven collection remain tracked separately. The current collector uses bounded periodic dumps. Its before-and-after checks cannot prove continuous Linux device identity between those observations.
