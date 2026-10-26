# Native static tunnels v0.1

Status: implementation and local regression checks in progress. Native CI and
browser evidence must qualify the exact source revision before acceptance.
This document does not declare a completed Phase 1 or close #43.

## Scope and workflow

The Phase 1 SW-09 / SW-12 workflow configures standalone static VXLAN, Geneve and
IPv4 GRE Ports. VXLAN and Geneve accept static numeric IPv4 or IPv6 endpoints.
Bridge → Port → Interface remains explicit. Creation allocates fresh immutable
identities; configuration preserves them; deletion recovery recreates the
captured configuration using reserved fresh identities.

The closed operations are `tunnel.static.create`, `tunnel.static.configure` and
`tunnel.static.delete`. They enter the shared Candidate, native Diff, Validation,
Safe Apply, confirmation and Event/Audit workflow. No page performs a live save.
The configurable native group is Interface.type plus remote_ip, local_ip, key
and dst_port. Omission preserves a captured option; null removes it. Recovery
retains native unset values, foreign persistent columns and private option maps
without inserting defaults or clamping values.

Dynamic remote_ip/key=flow, controller-owned rows, additional native options,
unsupported tunnel types, offload, DPDK and unqualified Bridge combinations remain
read-only. Unknown option values are not projected into the public response.
Static endpoints and local ofport/link observations do not prove remote
reachability. Host addresses, routes, IPsec, fabric automation and controller
provisioning are separate workflows.

## Authority and safety

The reserved capability `ovs.tunnel.static.write` is provisioned only by the
offline root actions `--grant-static-tunnel-role` / `--revoke-static-tunnel-role`.
Generic topology, VLAN or multicast permission does not substitute for it.
Existing sessions retain their original capability ceiling; current role
revocation invalidates execution authority.

Root grants use `--local-tunnel-objects` for explicit current management IDs and
`--local-tunnel-create-targets` for exact Bridge-management-ID:name pairs. A grant
change invalidates a staged draft, including remove-and-restore changes. A newly
created or restored Port/Interface requires explicit provisioning of its fresh
IDs before another update or deletion; names do not transfer existing ownership.

Admission uses the actual daemon-advertised iface_types, qualified persistent
schema, complete private configurations and native receive-identity checks.
Dynamic or advanced same-type tunnels cannot silently supply a guessed identity.
Atomic native transactions fence the complete persistent root/Bridge/Port/
Interface graph, advertised types and foreign references. Applied qualification
retains the captured dependency set and native proof. Compensation preserves the
admitted originals, requires current==our-after, checks runtime support and
receive identities, and refuses external changes. Lost replies never supply a
new target or trigger replay.

## Review and qualification

Desktop supports preparation and shared execution; tablet and mobile review
existing objects and transactions. Standard/Expert change depth only. Lists,
details, object links and scoped Events/Audit use authoritative service data.
Permission denial, stale observations, dynamic ownership, empty results and
retired identities are separate states.

The native test covers VXLAN IPv4/IPv6, Geneve IPv4/IPv6 and GRE IPv4 with real
kernel peers and traffic; it exercises create/confirm, changed keys, exact
configuration rollback, delete, manager database reopen, deadline recovery,
fresh identities, lost replies, no replay and external conflict. A separate
amd64/arm64 CI job runs three schema fixtures with distro OVS. The existing
attested OVS 3.3.9/3.7.1/4.0.0 builds also run the same five cases with each
version's own schema in default, enabled and sanitizer variants. These are
distinct kinds of evidence and must not be reported interchangeably.

Formal browser tests use the actual embedded Svelte frontend and Go services,
OVSDB and ovs-vswitchd. Required evidence includes CRUD review, confirmation and
compensation, reader/withheld/dynamic/stale states, Standard/Expert, light/dark,
tablet/mobile and new/retired identities. Acceptance requires every existing
gate, official artifact digests and actual screenshot review on both the source
revision and independently tested main. #71 remains deferred.
