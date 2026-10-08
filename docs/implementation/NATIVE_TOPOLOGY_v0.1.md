# Core native topology v0.1

2026-10-08 · #21 / #42 remaining implementation. API 1.24.0; validator `native-topology-v1`.

## Delivered semantics

The formal Svelte `/switching/topology` page reads the confirmed Go inventory and prepares a single semantic graph intent. Bridge → Port → Interface remains explicit; a Bond is a Port with multiple Interfaces. Object names and management identities cannot be renamed. New requests use Candidate → native Diff/Validation → High-risk Safe Apply → shared Jobs/Events/Audit, with no live object PATCH.

| Intent | Native result |
| --- | --- |
| `port.create` | Existing system Bridge gains a fresh Port and internal Interface, or an explicitly named existing unused Linux system device |
| `bond.create` | Fresh active-backup Port, LACP off, 2–8 existing Linux system devices; later Bond/LACP/VLAN edits use their accepted independent intents |
| `port.delete` | Non-local Port and its member Interfaces leave the graph; existing Linux system devices remain host objects |
| `bridge.delete-tree` | System Bridge, local Port/internal Interface and its admitted child graph leave the root set |
| `port.move` | Explicit source/destination Bridges change attachment; Port/Interface identities and all native configuration remain unchanged |
| `bond.members.set` | Complete explicit membership; additions consume explicitly selected standalone same-Bridge Ports with identical native VLAN semantics; removals become new standalone Ports under explicit name grants |
| `interface.ofport.set/clear` | Explicit request or native empty automatic request; the captured Bridge-wide actual/requested allocation must remain compatible |
| `interface.patch.connect/disconnect` | Two explicitly bound non-local standalone internal Interfaces become reciprocal Patch peers, or return to internal with empty peer options |

Existing isolated Bridge creation supplies root/local rows. Existing VLAN/QinQ, Bond/LACP, Interface MTU and ingress policing retain their accepted contracts and independent permissions. Splitting a Bond explicitly clears Bond mode, LACP and `lacp-fallback-ab` on standalone Ports while retaining VLAN and unknown maps. Type transitions require empty native MTU/ofport requests, disabled policing and the exact reciprocal/empty options shape. Tunnels, DPDK/offload activation and host address/device provisioning retain #43/#47/#54 domain workflows.

## Authority and bounded review

`ovs.topology.write` is an independent account capability. The root-owned manager configuration additionally requires:

- `--local-topology-objects=<management-id,...>` for every existing object read or written by the selected operation, maximum 256 grants.
- `--local-topology-create-targets=<bridge-management-id>:<immutable-name>,...` for every new Port/Interface name, maximum 128 grants.

These flags grant neither VLAN/MTU/policing nor general provider control. Empty grants deny writes. Controller-owned objects, non-system Bridges, STP/RSTP and foreign outgoing resource dependencies remain observable but cannot enter this core writer. All incoming schema references, including unmonitored weak references, are checked atomically; source membership is never implicitly stolen.

The topology selector returns at most 64 objects and 56 KiB; truncation explicitly disables new graph changes. The complete Interface inventory keeps its accepted snapshot pagination. An admitted transaction touches at most 32 distinct original/new objects, additionally constrained by existing Candidate, IPC, row/snapshot and execution-plan budgets. Oversized or incomplete review fails closed. Review original/current/yours, members, known native configuration and immutable identities before applying. Standard and Expert share server permissions; Expert adds identity/digest depth. New transactions require desktop; tablet/mobile retain review and existing Safe Apply responsibilities.

## Native images, atomic guards and recovery

The schema monitor privately captures persistent configuration columns of core tables, including unknown columns/maps. Ephemeral and documented daemon status/counter columns are excluded. Public inventory, Candidate, validation and evidence expose only known configuration summaries, immutable bindings and digests; private options or unknown values are never serialized into these resources. Private images travel only through manager-side inventory/execution transport and the protected durable execution plan, subject to existing file/peer access and size limits. They must not be logged or exported as public diagnostics.

The executor reconstructs the desired image from the typed semantic request and manager-captured current data. It verifies the sealed digest, full before configuration, root policy/counter, every incoming native reference, captured allocations and creation name/UUID absence in the same OVSDB transaction. Existing rows update only changed semantic columns. Strong references and root membership drive native GC. Unknown columns/maps survive; no caller supplies raw rows, transaction statements or recovery images.

Applied needs the exact native after image, root commit marker, target counter, valid reference graph, healthy Interface allocation/error fields and relevant Linux host observations. Requested OpenFlow port allocation must be actually observed. New system members require existing unattached Linux devices without routable addresses or upper devices. Internal deletion/type conversion refuses host network dependencies and verifies device removal. This service does not provision physical devices or change host IP configuration.

Admission reserves fresh management/OVS identities for every potentially deleted object before mutation, and locks the shared root plus touched objects. Rollback checks current equals our after, uses the private original checkpoint and remaps only core typed references to reserved replacements. Deleted identities stay retired; unchanged objects retain identities. External configuration, references or host dependencies cause an explicit rollback conflict. Lost replies preserve unknown outcomes and never authorize replay; process recovery re-observes the durable plan. Root revocation/current credential ceilings invalidate validation and normal execution authority; admitted recovery retains its sealed protection.

## Completion map

| Parent acceptance | Evidence and ownership |
| --- | --- |
| #21 full list/detail, type/ofport/admin/link, hardware/DPDK association, filtering and immutable references | Accepted Interface inventory/native types/native configuration/Linux device/selection batches; unsupported hardware associations are explicit, never inferred |
| #21 controlled fields, type/attachment, readable Standard and native exceptions | Accepted MTU/policing plus this semantic topology page, native Diff and shared evidence; general raw CRUD is unavailable |
| #42 create/delete/recreate, local/root rows and schema transactions | Accepted isolated Bridge/internal Port lifecycle plus this core graph lifecycle and fresh replacement registry |
| #42 Bond/LACP/VLAN/native fields, no member stealing | Accepted Bond/LACP and QinQ native batches plus explicit source admission/member split/merge; full VLAN membership page remains #22/#54 |
| #42 common change control, management/sole-uplink risk, external change/revocation/recovery | Existing shared protections plus topology guards, native kernel management-path-loss recovery and independent capability tests |

This map does not close independent domain or full-page issues and does not assert physical LACP, NIC/DPDK/offload performance acceptance. Three schema fixtures execute against the installed OVS daemon, not three different daemon releases. Review and final disposition: [matrix](../reviews/NATIVE_TOPOLOGY_v0.1.md).
