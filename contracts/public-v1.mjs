// Public transport source. Service availability is distinct from wire coverage.
import { readFileSync } from 'node:fs';
const proposal = JSON.parse(readFileSync(new URL('./proposals/phase1-v1.openapi.json', import.meta.url)));
export const api = structuredClone(proposal);
api.info = { title: 'OVS WebUI public API', version: '1.35.0', description: 'Phase 1 typed VLAN, Bond/LACP and managed isolated Bridge creation/deletion through Candidate and durable Safe Apply. Deletion compensation uses fresh identities and shared evidence. Independent internal access Port creation and deletion on an explicitly authorized existing Bridge are also supported; deletion recovery preserves the parent and uses fresh child identities. QinQ and customer VLAN lists are supported on eligible existing single-interface system Ports with the native TPID preserved. Interface inventory exposes native field provenance, requested versus observed MTU and reported device status through list/detail pages. Explicit and proven automatic MTU transitions on authorized standalone non-local internal Interfaces use Candidate and Safe Apply with actual device MTU proof and sealed default dependencies. Interface detail adds bounded, independently sourced Linux device observations and exact sysfs hardware associations after matching the native name and ifindex. Interface configuration observes requested port allocation and four ingress policing parameters with explicit native units, unsupported schemas and configuration permissions; configuration does not prove runtime allocation or enforcement. Interface patch configuration resolves reciprocal peers within a fresh snapshot without inferring operational connectivity. Interface MTU lifecycle evidence carries bounded, server-derived immutable target references for shared object-scoped Events/Audit navigation; retained history is not complete history. Interface detail independently observes bounded Linux ingress tc police actions, with explicit kernel units, partial coverage and identity/configuration permission gates. Installed rules are not measured traffic effectiveness or installer ownership. Core system/internal graph creation, deletion, attachment, explicit Bond membership, ofport allocation and paired internal/patch transitions use independent topology authority and High-risk Safe Apply. Tunnel configuration, DPDK/offload activation and host network provisioning retain separate domain gates.' };
api.info.description += ' Controlled ingress policing supports one bandwidth or packet rate, or disable, on independently authorized standalone internal Interfaces with native default bursts preserved. A root policy declares exclusive software ingress management. Dispatch, Applied and compensation require qualified kernel and guarded native configuration evidence; neither proves measured traffic effectiveness.';
api['x-review-status'] = 'implementation-review';
api.info.description += ' Spanning-tree observation uses a coherent Bridge/Port snapshot, separates configured protocol and unset parameters from daemon-reported state/role, preserves withheld/unknown/stale/unsupported states, and exposes mutual-enable conflicts without resolving them. Native Bond/internal exclusions and unresolved mirror output/type participation remain explicit. Observation never grants write authority. A separate opt-in bridge.spanning-tree.configure intent requires explicit capability/root grants, a qualified native witness, whole-graph CAS and durable Safe Apply. Applied requires actual exclusive protocol/local installation, and compensation preserves raw/unset originals; network convergence is not inferred. Preparation stays non-executable.';
api['x-contract-baseline'] = 'v1.0.0';
api.servers = [{ url: '/api/v1' }];
const s = api.components.schemas;
const ref = name => ({ $ref: `#/components/schemas/${name}` });
const string = (maxLength = 128) => ({ type: 'string', minLength: 1, maxLength });
const integer = (minimum, maximum) => ({ type: 'integer', minimum, maximum });
const array = (items, maxItems = 128, minItems = 0) => ({ type: 'array', items, minItems, maxItems });
const nullable = schema => ({ anyOf: [schema, { type: 'null' }] });
const closed = properties => ({ type: 'object', properties, required: Object.keys(properties), additionalProperties: false });
const open = properties => ({ type: 'object', properties, required: Object.keys(properties), additionalProperties: true });
const choices = (...values) => ({ type: 'string', enum: values });
const command = properties => closed({ request_id: ref('RequestId'), ...properties });
const bool = { type: 'boolean' };
const id = ref('Id');
const object = ref('ObjectBinding');
const revision = ref('Revision');
// Additive Candidate/Validation read models; frozen v1 requests stay compatible.
Object.assign(s.Candidate.properties, {
 safe_apply_available: bool,
 current_instance_generation: nullable(id), current_config_revision: nullable(revision), conflict_snapshot_id: nullable(id),
 diff: array(ref('DiffField'), 512), checks: array(ref('Gate'), 512), diff_truncated: bool,
});
Object.assign(s.ObservedIntent.properties, { before: ref('NativeVlan'), dependency_revision: revision, schema_digest: string() });
Object.assign(s.DiffField.properties, { current: {}, operation: string(), intent_id: id, conflict: bool });
Object.assign(s.Gate.properties, { intent_id: id });
Object.assign(s.Validation.properties, {
 changeset_id: id, usable: bool, invalidations: array(ref('Gate'), 512), risk: string(), execution_ready: bool,
});
s.Validation.description = 'Immutable evaluation state with dynamically rechecked usable/invalidations. A successful Job can contain a blocked validation. Candidate, credential, policy, generation, schema, touched fields and dependencies invalidate use. Actual dispatch remains a separate preflight gate.';

const strMap = { type: 'object', maxProperties: 32, additionalProperties: string(1024) };
s.Id = { type: 'string', format: 'uuid', pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' };
s.RequestId = { ...s.Id, pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' };
s.Revision = { type: 'string', minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9_-]+$' };
s.Resource = open({ id, state: string(), sequence: ref('Sequence'), resource_kind: string(), source: ref('Source'), allowed_actions: array(string(), 64) });
s.ResourcePage = open({ snapshot_id: id, instance_generation: nullable(id), items: array(ref('Resource'), 500), next_cursor: nullable(string(4096)), truncated: bool });
s.Bridge = open({ ...s.Resource.properties, management_id: id, ovs_uuid: id, instance_generation: id, config_revision: revision, name: string(), port_refs: array(ref('ResourceRef'), 500), datapath_type: string() });
s.Interface = open({ ...s.Resource.properties, management_id: id, ovs_uuid: id, instance_generation: id, config_revision: revision, name: string(), port_ref: ref('ResourceRef'), interface_type: string(), options: { type: 'object', additionalProperties: true } });
s.InterfacePatchPeer = open({availability: string(), reason: {type:'string', maxLength:128}, peer_ref: nullable(ref('ResourceRef')), peer_port_ref: nullable(ref('ResourceRef')), peer_bridge_ref: nullable(ref('ResourceRef')), source: ref('Source')});
s.InterfacePatchPeer.description = 'Read-only reciprocal patch configuration resolved within one fresh confirmed inventory snapshot and known native schema. Missing, ambiguous, one-way, incompatible-datapath, stale or unauthorized observations expose no peer identity. configuration.read is required even for applicability. This is configuration evidence, never packet forwarding, hardware ownership or write authority.';
s.Interface.properties.patch_peer = ref('InterfacePatchPeer');
s.InventoryField = open({ value: {}, availability: string(), source: ref('Source'), schema_mutable: bool, ownership: string(), editable: bool });
s.InventoryField.properties.reason = {type:'string', maxLength:128};
s.InventoryField.description = 'Native field with independent authority and observation provenance. Selected Interface configuration requests distinguish known native empty/zero, unsupported schema shapes, unknown observations and configuration withheld. Schema mutability grants no write authority; configuration values do not prove runtime enforcement.';
const treeFields = { type: 'object', maxProperties: 32, additionalProperties: ref('InventoryField') };
s.SpanningTreeBridgeObservation = open({ protocol: { ...string(), 'x-known-values':['unknown','disabled','stp','rstp','invalid-both-enabled'] }, availability: string(), reason: {type:'string',maxLength:128}, configuration: treeFields, runtime: treeFields, source: ref('Source'), ownership: string(), editable: bool, write_reason: string() });
s.SpanningTreeParameterCheck = closed({ code: string(), fields: array(string(), 2) });
s.SpanningTreeParameterValidation = closed({ version: string(), scope: {type:'string',const:'bridge-basic-parameters'}, state: { ...string(), 'x-known-values':['valid','invalid','runtime-unverified','unknown','unsupported','withheld','stale'] }, checks: array(ref('SpanningTreeParameterCheck'),12) });
s.SpanningTreeParameterValidation.description = 'Read-only assessment of seven basic Bridge parameters for both STP and RSTP, including inactive settings. Absent keys use native defaults only as validator inputs; raw observation remains unset. Valid is not installed timer evidence, authority, graph eligibility, safe transition or execution readiness. Permission-withheld, stale, unknown and unsupported inputs disclose no checks. Advanced Bridge and Port parameters retain separate gates.';
s.SpanningTreeBridgeObservation.properties.parameter_validation = ref('SpanningTreeParameterValidation');
s.SpanningTreePortObservation = open({ configuration: treeFields, runtime: treeFields, stp_participation: ref('InventoryField'), rstp_participation: ref('InventoryField'), source: ref('Source'), ownership: string(), editable: bool });
s.Bridge.properties.spanning_tree = ref('SpanningTreeBridgeObservation');
s.SpanningTreePort = open({ name: string(), port_ref: ref('ResourceRef'), spanning_tree: ref('SpanningTreePortObservation') });
s.SpanningTree = open({ ...s.Resource.properties, management_id: id, ovs_uuid: id, instance_generation: id, config_revision: revision, snapshot_id: id, name: string(), bridge_ref: ref('ResourceRef'), spanning_tree: ref('SpanningTreeBridgeObservation'), ports: array(ref('SpanningTreePort'),128), ports_truncated: bool, port_count: integer(0,500), mode: { ...string(), 'x-known-values':['observe'] }, editable: bool, allowed_operations: array(string(),64), write_reason: string() });
s.SpanningTree.description = 'One Bridge management identity, never a separate native STP object. Detail Ports come from this same immutable snapshot, ordered by natural name and bounded by 128 rows / 52 KiB; ports_truncated signals partial detail and Port/Interface links retain access to remaining inventory. List resources omit Port detail. Empty optional native parameters are unset, never fabricated defaults; absent runtime keys are unknown. Runtime provenance/freshness is independent from configured enablement and does not establish forwarding or convergence. Read requires inventory.read and state.read; configuration/type/participation/authority additionally require configuration.read. A native-enabled port assessment applies only when its Bridge protocol is enabled. All writes retain independent #23/#43 gates.';
Object.assign(s.Interface.properties, { bridge_ref: ref('ResourceRef'), port_kind: string(), internal: nullable(bool), local_interface: nullable(bool), ownership: string(), allowed_operations: array(string(), 64), fields: { type: 'object', additionalProperties: ref('InventoryField') } });
s.Interface.description = 'Native Interface inventory with explicitly admitted MTU edits. Configuration fields require configuration.read; operational fields remain observations. Empty optional values, unavailable columns and withheld values remain distinct. No hardware role or Interface type is inferred from names. MTU editing requires independent root Interface authority on a standalone non-local internal Interface. Empty native requests require proven default dependencies.';
Object.assign(s.Interface.properties, { mtu_ownership: string(), mtu_editable: bool, mtu_clearable: bool, mtu_default: integer(576,65535), mtu_default_reason: {type:'string', maxLength:256} });
Object.assign(s.Interface.properties, { policing_ownership: string(), policing_editable: bool });
s.LinuxDeviceField = open({value: {}, availability: string(), reason: {type:'string', maxLength:128}});
s.LinuxDeviceObservation = open({availability: string(), reason: {type:'string', maxLength:128}, ifindex: nullable(integer(1,2147483647)), source: ref('Source'), fields: {type:'object', maxProperties:10, additionalProperties:ref('LinuxDeviceField')}});
s.LinuxDeviceObservation.description = 'Detail-only read observation. Native OVS name/type/ifindex and confirmed, fresh identity gate a bounded Linux sysfs sample; association is rechecked after reading. Carrier, operstate, MTU and speed remain distinct from OVS values. PCI requires the first actual ancestor subsystem to be PCI; OVS bus_info is only a reported hint. Unavailable fields are not replaced with defaults. No paths, host attachment candidates, write authority, caching or device mutation.';
s.LinuxHardwareField = closed({ value: nullable(string(64)), availability: choices('known','unknown'), reason: {type:'string', maxLength:128}, source: ref('Source') });
s.LinuxHardwareObservation = closed({ pci_function: ref('LinuxHardwareField'), switch_port: ref('LinuxHardwareField'), connector: ref('LinuxHardwareField') });
s.LinuxHardwareObservation.description = 'Separate observations of the associated SR-IOV PCI function, the driver-declared devlink port relation, and the driver-reported ethtool port medium. Each has its own provenance and observation time. Missing proof stays unknown. Physical chassis mapping, forwarding, native OVS type and write authority are independent.';
s.LinuxDeviceObservation.properties.hardware = ref('LinuxHardwareObservation');
s.Interface.properties.linux_device = ref('LinuxDeviceObservation');
const kernelRate = nullable({type:'string', pattern:'^(0|[1-9][0-9]{0,19})$', maxLength:20});
s.LinuxPolicingAction = open({filter_kind:choices('basic','matchall','u32'), priority:integer(0,65535), handle:string(10), index:integer(0,4294967295), bytes_per_second:kernelRate, packets_per_second:kernelRate, exceed_action:string(32), conform_action:string(32)});
s.LinuxIngressPolicing = open({availability:choices('known','partial','unavailable','withheld'), reason:{type:'string',maxLength:128}, ifindex:nullable(integer(1,2147483647)), filter_count:nullable(integer(0,64)), actions:array(ref('LinuxPolicingAction'),16), coverage:choices('linux-tc-ingress'), source:ref('Source')});
s.LinuxIngressPolicing.description = 'Optional detail-only, read-only Linux ingress tc observation, independently sourced from NETLINK_ROUTE. Requires inventory/state/configuration read and a fresh confirmed native system/internal name and ifindex, checked before and after reading. Bounded basic/matchall/u32 police decoding; unknown classifiers/actions, future or peak/average police semantics and shared ingress blocks yield partial coverage. Errors, interruption, truncation, stale or replaced identities publish no actions. Rates are exact uint64 decimal strings in kernel bytes/s and packets/s, never converted OVS configuration units. Null means not reported, not disabled. Burst tick conversion, egress, XDP, userspace/DPDK, hardware enforcement, traffic effectiveness and installer ownership are not established. No writes, allowed operations, list scans, cache, dispatch proof or configuration revision changes.';
s.Interface.properties.linux_ingress_policing = ref('LinuxIngressPolicing');

s.Bond = open({ ...s.Port.properties, lacp: string(), bond_mode: string(), member_refs: array(ref('ResourceRef'), 128) });
s.User = open({ id, revision, username: string(), disabled: bool, role_ids: array(id, 64) });
s.Role = open({ id, revision, name: string(), capabilities: array(string(), 128) });
s.Token = open({ id, name: string(), scopes: array(string(), 128), expires_at: ref('DateTime'), revoked: bool });
s.TokenCreated = open({ ...s.Token.properties, secret: { type: 'string', 'x-sensitive': true }, secret_available: bool });
s.Metadata = open({ id, revision, kind: string(), name: string(), description: { type: 'string', maxLength: 4096 } });
s.Runtime = open({ state: string(), scope: string(), configuration_ready: bool, authentication_ready: bool });
s.ContractInfo = open({ major: { type: 'integer', const: 1 }, version: string(), openapi_url: string(), service_state: string(), request_domains: array(string(), 2) });
s.Subscription = closed({ resources: array(closed({ kind: string(64), id }), 32, 1), cursor: nullable(closed({ stream_id: id, sequence: ref('Sequence') })) });

s.QinQContext = open({ vlan_limit: { type: 'string' }, datapath_uuid: { type: 'string' }, max_vlan_headers: { type: 'string' }, dependency_revision: revision, ethertype: nullable({ type: 'string' }) });
s.ObservedIntent.properties.qinq_context = ref('QinQContext');
Object.assign(s.Port.properties, { qinq_editable: bool, qinq_ethertype: nullable({ type: 'string' }) });
// Switching mutations remain Candidate intents, never live PATCH on OVS objects.
s.InterfaceSpec = closed({ name: string(), type: choices('system', 'internal', 'patch', 'vxlan', 'geneve', 'gre'), options: strMap });
const intent = (operation, properties) => closed({ intent_id: id, operation: { type: 'string', const: operation }, ...properties });
const fallback = choices('preserve', 'enabled', 'disabled', 'default');
const topologyOperations = ['port.create','bond.create','port.delete','bridge.delete-tree','port.move','bond.members.set','interface.ofport.set','interface.ofport.clear','interface.patch.connect','interface.patch.disconnect'];
s.TopologyRequest = { type:'object', additionalProperties:false, properties:{ name:{type:'string',minLength:1,maxLength:15,pattern:'^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$'}, native_type:choices('internal','system'), interface_names:array(string(15),8), source_ports:array(object,8), destination_bridge:object, member_interfaces:array(object,8,1), peer:object, ofport_request:integer(1,65279) } };
s.TopologyNode = open({binding:object,name:string(),native_type:{type:'string'},links:array(object,128),configuration_digest:revision});
s.TopologyNode.properties.configuration_summary = {type:'object',maxProperties:16,additionalProperties:{}};
s.TopologyChange = open({root_uuid:id,root_dependency:revision,before:array(ref('TopologyNode'),32),after:array(ref('TopologyNode'),32),restored:array(ref('TopologyNode'),32),replacements:{type:'object',additionalProperties:object},request:ref('TopologyRequest')});
s.ObservedIntent.properties.topology_change=ref('TopologyChange');
const intentSchemas = {
 BridgeCreateIntent: intent('bridge.create', { management_id: id, name: string(), datapath_type: choices('system', 'netdev') }),
 BridgeRemoveIntent: intent('bridge.remove', { object }),
 PortCreateIntent: intent('port.create', { management_id: id, bridge_id: id, name: string(), interfaces: array(ref('InterfaceSpec'), 32, 1) }),
 PortRemoveIntent: intent('port.remove', { object }),
 InterfaceOptionsIntent: intent('interface.configure', { object, options: strMap, mtu: nullable(integer(576, 65535)) }),
 BondIntent: intent('bond.configure', { object, mode: choices('active-backup', 'balance-slb', 'balance-tcp'), lacp: choices('off', 'active', 'passive'), member_interface_ids: array(id, 32, 2) }),
 SpanningTreeIntent: intent('spanning_tree.configure', { object, protocol: choices('disabled', 'stp', 'rstp'), priority: integer(0, 65535), forward_delay: integer(4, 30), hello_time: integer(1, 10), max_age: integer(6, 40) }),
 MulticastIntent: intent('multicast.configure', { object, snooping: bool, flood_unregistered: bool }),
 MirrorIntent: intent('mirror.configure', { object, name: string(), source_port_ids: array(id), destination_port_ids: array(id), output_port_id: nullable(id), output_vlan: nullable(integer(1, 4094)) }),
 QoSIntent: intent('qos.configure', { object, type: choices('linux-htb', 'linux-hfsc'), max_rate_bps: integer(1, 9007199254740991), queues: array(closed({ queue_id: integer(0, 4294967295), min_rate_bps: integer(0, 9007199254740991), max_rate_bps: integer(1, 9007199254740991) }), 128) }),
 IsolationIntent: intent('isolation.configure', { object, protected: bool }),
 TelemetryIntent: intent('telemetry.configure', { object, protocol: choices('sflow', 'netflow', 'ipfix'), targets: array(string(256), 16), sampling: integer(1, 10000000), polling_seconds: integer(1, 3600) }),
 ProfileAssignmentIntent: intent('profile.assign', { object, profile_id: id, profile_revision: revision }),
 ManagementNetworkIntent: intent('management_network.configure', { object, mtu: integer(576, 65535), addresses: array(string(128), 32), gateways: array(string(128), 16) }),
 ControllerIntent: intent('controllers.configure', { object, targets: array(string(256), 16), fail_mode: choices('standalone', 'secure') }),
 PortLACPIntent: intent('port.lacp.set', { object, lacp: choices('off', 'active', 'passive'), fallback }),
 IsolatedBridgeIntent: intent('bridge.create-isolated', { name: { type: 'string', minLength: 1, maxLength: 15, pattern: '^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$' } }),
 IsolatedBridgeDeleteIntent: intent('bridge.delete-isolated', { object }),
 InternalPortDeleteIntent: intent('port.delete-internal', { object }),
 InternalPortCreateIntent: intent('port.create-internal', { object, name: {type: 'string', minLength: 1, maxLength: 15, pattern: '^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$'}, vlan_id: integer(1,4094) }),
 InterfaceMTUIntent: intent('interface.mtu.set', { object, mtu_request: integer(576,65535) }),
 InterfaceMTUClearIntent: intent('interface.mtu.clear', { object }),
 InterfacePolicingIntent: intent('interface.policing.set', { object, policing: ref('PolicingRequest') }),
};
intentSchemas.TopologyIntent = closed({intent_id:id,operation:choices(...topologyOperations),object,topology:ref('TopologyRequest')});
intentSchemas.TopologyIntent.description='Typed High-risk core topology operations through Candidate and Safe Apply only. Every touched object and new name requires independent root authority. Source Ports and member Interfaces use immutable bindings; no implicit member stealing. Native before/after waits, foreign-reference checks, daemon allocation proof and fresh compensation identities are mandatory. No generic rows, tunnel/DPDK/offload or host provisioning writes.';
intentSchemas.SpanningTreePrepareIntent = intent('bridge.spanning-tree.prepare', { object, spanning_tree: ref('SpanningTreePrepareRequest') });
intentSchemas.SpanningTreeConfigureIntent = intent('bridge.spanning-tree.configure', { object, spanning_tree: ref('SpanningTreePrepareRequest') });
intentSchemas.PortIsolationIntent = intent('port.isolation.set', { object, protected: bool });
intentSchemas.PortIsolationIntent.description = 'One native protected Port field. Requires explicit ovs.port.isolation.write and independent root immutable Port ownership, coherent system Bridge members, sealed originals and Safe Apply. Protected ports do not forward to other protected ports on the same Bridge under native NORMAL switching; this is not a firewall or general PVLAN. Unsupported domain combinations remain blocked.';
s.IsolationChange=closed({before:bool,after:bool});
s.ObservedIntent.properties.isolation_change=ref('IsolationChange');
s.IsolationExecution=closed({available:bool,reason:{type:'string',maxLength:128},authority:string(),capability:{type:'string',const:'ovs.port.isolation.write'}});
s.Port.properties.isolation_execution=ref('IsolationExecution');
s.MulticastParameters=closed(Object.fromEntries(['mcast-snooping-aging-time','mcast-snooping-table-size','mcast-snooping-disable-flood-unregistered'].map(k=>[k,nullable({type:'string',maxLength:128})])));
s.MulticastNativeConfig=closed({enabled:bool,parameters:ref('MulticastParameters')});
s.MulticastRequest=closed({enabled:bool,parameters:{type:'object',additionalProperties:false,properties:{'mcast-snooping-aging-time':nullable({type:'string',minLength:1,maxLength:4,pattern:'^[0-9]+$'}),'mcast-snooping-table-size':nullable({type:'string',minLength:1,maxLength:7,pattern:'^[0-9]+$'}),'mcast-snooping-disable-flood-unregistered':nullable(choices('true','false'))}}});
s.MulticastChange=closed({before:ref('MulticastNativeConfig'),after:ref('MulticastNativeConfig')});
s.MulticastExecution=closed({available:bool,reason:{type:'string',maxLength:128},authority:string(),capability:{type:'string',const:'ovs.bridge.multicast.write'}});
s.Bridge.properties.multicast_config=nullable(ref('MulticastNativeConfig'));
s.Bridge.properties.multicast_execution=ref('MulticastExecution');
s.MulticastGroup=closed({ofport:integer(1,65534),vlan:integer(0,4094),protocol:choices('IGMPv1','IGMPv2','IGMPv3','MLDv1','MLDv2','UNKNOWN'),address:{type:'string',maxLength:45},router:bool,age_seconds:integer(0,4294967295),port_ref:ref('ResourceRef'),port_name:string(128)});
s.MulticastGroups=closed({availability:choices('known','unavailable','withheld'),reason:{type:'string',maxLength:128},items:nullable(array(ref('MulticastGroup'),256)),source:ref('Source')});
s.MulticastGroups.description='A bounded, separate native mdb/show observation under an explicitly pinned optional OVS UUID/name witness build. Rechecks daemon, DB source, Bridge binding, installed/IDL epochs and the current persistent graph/ofport association. Unavailable/withheld returns null items, never an empty list; an empty array is a successfully observed empty native membership table. Configuration is not measured multicast delivery.';
s.Bridge.properties.multicast_groups=ref('MulticastGroups');
s.ObservedIntent.properties.multicast_change=ref('MulticastChange');
intentSchemas.BridgeMulticastIntent=intent('bridge.multicast.configure',{object,multicast:ref('MulticastRequest')});
intentSchemas.BridgeMulticastIntent.description='Closed native Bridge snooping flag and three basic native parameters under independent capability/root Bridge ownership. Preserves original unset keys and unrelated maps, reserves and atomically guards the persistent graph, and uses shared Safe Apply and exact guarded compensation. Configuration is not multicast group membership or measured packet-delivery proof. Legacy multicast.configure remains unsupported.';
// Static tunnels are a separate closed domain; core topology grants do not apply.
s.StaticTunnelOptions={type:'object',additionalProperties:false,properties:{remote_ip:nullable(string(128)),local_ip:nullable(string(128)),key:nullable({type:'string',minLength:1,maxLength:10,pattern:'^[0-9]+$'}),dst_port:nullable({type:'string',minLength:1,maxLength:5,pattern:'^[0-9]+$'})}};
s.StaticTunnelNativeOptions={type:'object',additionalProperties:false,properties:Object.fromEntries(['remote_ip','local_ip','key','dst_port'].map(k=>[k,{type:'string',maxLength:128}]))};
s.StaticTunnelConfiguration=closed({native_type:string(32),options:ref('StaticTunnelNativeOptions')});
s.StaticTunnelCreateRequest=closed({native_type:choices('vxlan','geneve','gre'),options:ref('StaticTunnelOptions')});
s.StaticTunnelPatchRequest={type:'object',additionalProperties:false,required:['options'],properties:{native_type:choices('vxlan','geneve','gre'),options:ref('StaticTunnelOptions')}};
s.TopologyRequest.properties.tunnel=ref('StaticTunnelPatchRequest');
s.StaticTunnelChange=closed({interface:object,before:nullable(ref('StaticTunnelConfiguration')),after:nullable(ref('StaticTunnelConfiguration'))});
s.TopologyChange.properties.tunnel_change=ref('StaticTunnelChange');
s.TopologyChange.properties.tunnel_authority_revision=revision;
s.TopologyChange.properties.tunnel_dependency=revision;
intentSchemas.StaticTunnelCreateIntent=intent('tunnel.static.create',{object,topology:closed({name:s.TopologyRequest.properties.name,tunnel:ref('StaticTunnelCreateRequest')})});
intentSchemas.StaticTunnelConfigureIntent=intent('tunnel.static.configure',{object,topology:closed({tunnel:ref('StaticTunnelPatchRequest')})});
intentSchemas.StaticTunnelDeleteIntent=intent('tunnel.static.delete',{object,topology:closed({})});
for(const key of ['StaticTunnelCreateIntent','StaticTunnelConfigureIntent','StaticTunnelDeleteIntent']) intentSchemas[key].description='Native static VXLAN/Geneve/GRE only, under independent ovs.tunnel.static.write and immutable root object/name grants. No flow endpoints/keys, controller-owned or unqualified advanced combinations. Shared Candidate, exact persistent graph CAS, runtime type admission, immutable receive-identity checks, durable reservations, Safe Apply and fresh-identity deletion compensation. No underlay, host addressing or OpenFlow provisioning.';
s.StaticTunnelExecution=closed({available:bool,reason:{type:'string',maxLength:128},capability:{type:'string',const:'ovs.tunnel.static.write'}});
s.StaticTunnelObservation=closed({configuration:nullable(ref('StaticTunnelConfiguration')),configuration_availability:choices('known','unknown','unsupported'),reason:{type:'string',maxLength:128},interface_binding:nullable(object),interface_ref:nullable(ref('ResourceRef')),bridge_ref:nullable(ref('ResourceRef')),native_type:nullable(string(32)),operational:nullable({type:'object',additionalProperties:true}),source:ref('Source'),execution:ref('StaticTunnelExecution')});
s.Resource.properties.native_tunnel=ref('StaticTunnelObservation');
api.info.description += ' Native static VXLAN/Geneve/GRE Port lifecycle uses independent root object/name policy and capability, whole-graph CAS, explicit runtime type support and shared Safe Apply. Dynamic/controller-owned/advanced tunnels remain observable and read-only. No underlay or host network provisioning is performed.';
// Native Mirror remains a separate object and authority domain. The frozen
// core ObjectBinding enum is preserved; new graph bindings are additive.
s.MirrorObjectBinding=closed({...structuredClone(s.ObjectBinding.properties),table:{type:'string',const:'Mirror'}});
s.NativeGraphBinding={anyOf:[object,ref('MirrorObjectBinding')]};
s.TopologyNode.properties.binding=ref('NativeGraphBinding');
s.TopologyChange.properties.replacements.additionalProperties=ref('NativeGraphBinding');
s.MirrorConfiguration=closed({select_all:bool,source_port_ids:{...array(id,32),uniqueItems:true},destination_port_ids:{...array(id,32),uniqueItems:true},select_vlans:{...array({type:'string',maxLength:4,pattern:'^(0|[1-9][0-9]{0,3})$'},256),uniqueItems:true},output_port_id:nullable(id),output_vlan:nullable({type:'string',maxLength:4,pattern:'^[1-9][0-9]{0,3}$'}),snaplen:nullable({type:'string',maxLength:5,pattern:'^[1-9][0-9]{0,4}$'})});
s.MirrorConfiguration.description='Native source and destination Port selection are independent directions. select_all preserves an independent VLAN filter; 0 denotes untagged traffic. Exactly one output Port or RSPAN VLAN is required. Native ranges, current same-Bridge Port bindings, reserved output conflicts and graph eligibility are checked by the service. snaplen null preserves native unset. Copy configuration and counters do not establish packet delivery.';
const mirrorName={type:'string',minLength:1,maxLength:64};
s.MirrorTopologyRequest={type:'object',additionalProperties:false,properties:{name:mirrorName,mirror:ref('MirrorConfiguration')}};
s.TopologyChange.properties.request={anyOf:[ref('TopologyRequest'),ref('MirrorTopologyRequest')]};
s.MirrorChange=closed({object:ref('MirrorObjectBinding'),bridge:object,before:nullable(ref('MirrorConfiguration')),after:nullable(ref('MirrorConfiguration'))});
s.TopologyChange.properties.mirror_change=ref('MirrorChange');
s.TopologyChange.properties.mirror_authority_revision=revision;
s.TopologyChange.properties.mirror_dependency=revision;
intentSchemas.NativeMirrorCreateIntent=intent('mirror.session.create',{object,topology:closed({name:mirrorName,mirror:ref('MirrorConfiguration')})});
intentSchemas.NativeMirrorConfigureIntent=intent('mirror.session.configure',{object:ref('MirrorObjectBinding'),topology:closed({mirror:ref('MirrorConfiguration')})});
intentSchemas.NativeMirrorDeleteIntent=intent('mirror.session.delete',{object:ref('MirrorObjectBinding'),topology:closed({})});
intentSchemas.QoSDetachIntent=intent('qos.detach',{object,qos:ref('QoSNativeBinding')});
intentSchemas.QoSAttachIntent=intent('qos.attach',{object,qos:ref('QoSNativeBinding')});
const qosProfileName={...string(32),pattern:'^[a-z][a-z0-9-]{0,31}$'};
s.QoSCreateQueue=closed({queue_id:integer(0,61439),min_rate_bps:integer(8,4294967295),max_rate_bps:integer(8,4294967295)});
intentSchemas.QoSCreateIntent=intent('qos.create',{instance_generation:id,name:qosProfileName,type:{type:'string',const:'linux-hfsc'},max_rate_bps:integer(12000,4294967295),queues:array(ref('QoSCreateQueue'),32,1)});
for(const key of ['NativeMirrorCreateIntent','NativeMirrorConfigureIntent','NativeMirrorDeleteIntent']) intentSchemas[key].description='Native Mirror lifecycle under independent ovs.mirror.write, immutable root object/name grants, sealed source/destination/VLAN/output selectors and whole five-table native CAS. Shared Candidate, durable Safe Apply and exact guarded compensation; deleted sessions recover with fresh identities. Advanced filters, external control and unqualified graph combinations stay read-only. Legacy mirror.configure is unsupported.';
s.MirrorExecution=closed({available:bool,reason:{type:'string',maxLength:128},capability:{type:'string',const:'ovs.mirror.write'}});
s.MirrorPort=closed({binding:object,name:{type:'string',maxLength:128},editable:bool,output_eligible:bool});
s.MirrorObservation=closed({configuration:nullable(ref('MirrorConfiguration')),configuration_availability:choices('known','unknown','unsupported'),reason:{type:'string',maxLength:128},bridge_ref:nullable(ref('ResourceRef')),ports:nullable(array(ref('MirrorPort'),32)),ports_truncated:bool,port_count:nullable(integer(0,4096)),source:ref('Source'),execution:ref('MirrorExecution')});
s.Resource.properties.native_mirror=ref('MirrorObservation');
s.QoSRate=closed({state:choices('configured','unset','unknown','not-applicable'),bps:nullable({type:'string',pattern:'^[0-9]+$',maxLength:20})});
s.QoSQueue=closed({queue_id:integer(0,4294967295),queue_ref:ref('ResourceRef'),min_rate:ref('QoSRate'),max_rate:ref('QoSRate'),dscp:nullable(integer(0,63)),dscp_state:choices('configured','unset','unknown')});
s.QoSPort=closed({port_ref:ref('ResourceRef'),name:string()});
s.QoSObservation=closed({
 discipline:nullable({type:'string',maxLength:128}), max_rate:ref('QoSRate'),
 binding_state:choices('unbound','bound','shared','unknown'), port_count:nullable(integer(0,2048)),
 ports:nullable(array(ref('QoSPort'),32)), ports_truncated:bool,
 queue_count:nullable(integer(0,128)), queues:nullable(array(ref('QoSQueue'),128)),
});
s.QoSObservation.description='Observed native configuration only. Null counts denote unknown coverage, zero denotes a known empty relationship. List results omit detail arrays. Scheduler installation, statistics and queue use require independent runtime evidence.';
s.QoSObservation.properties.name=qosProfileName;
s.Resource.properties.native_qos=ref('QoSObservation');
const qosCounter=nullable({type:'string',pattern:'^(0|[1-9][0-9]*)$',maxLength:20});
s.LinuxQoSQueue=closed({queue_id:integer(0,61439),bytes:qosCounter,packets:qosCounter,drops:qosCounter});
s.LinuxQoSObservation=closed({
 scheduler:choices('observed','not-observed','different','unavailable'), reason:{type:'string',maxLength:128},
 root_kind:nullable(string(31)), port_ref:nullable(ref('ResourceRef')),
 queues:array(ref('LinuxQoSQueue'),32), source:ref('Source'),
});
s.LinuxQoSObservation.description='Detail-only bounded Linux egress observation. Queue counters are cumulative software class statistics from the associated single Port. Null counters mean unreported. A matching scheduler layout does not establish configuration ownership or traffic delivery.';
s.LinuxQoSInstalledQueueRate=closed({queue_id:integer(0,61439),minimum_bps:{type:'string',pattern:'^[1-9][0-9]*$',maxLength:20},maximum_bps:{type:'string',pattern:'^[1-9][0-9]*$',maxLength:20}});
s.LinuxQoSInstalledRates=closed({maximum_bps:{type:'string',pattern:'^[1-9][0-9]*$',maxLength:20},queues:array(ref('LinuxQoSInstalledQueueRate'),32)});
s.LinuxQoSObservation.properties.installed_rates=ref('LinuxQoSInstalledRates');
s.LinuxQoSInstalledRates.description='Optional Linux HTB or linear HFSC class rates in bit/s, sampled independently of configured rates. Omitted if the expected software class tree is incomplete or the rates cannot be verified.';
s.Resource.properties.linux_qos=ref('LinuxQoSObservation');
s.QoSRateEdit=closed({available:bool,reason:string(128),port:nullable(object),revision:nullable(revision)});
s.Resource.properties.qos_rate_edit=ref('QoSRateEdit');
s.Resource.properties.qos_detach=ref('QoSRateEdit');
s.QoSAttachPort=closed({port:object,name:string()});
s.QoSAttachStatus=closed({available:bool,reason:string(128),revision:nullable(revision),ports:array(ref('QoSAttachPort'),1)});
s.QoSCreateStatus=closed({available:bool,reason:string(128),instance_generation:nullable(id),revision:nullable(revision),aliases:{...array(qosProfileName,64),uniqueItems:true}});
s.Resource.properties.qos_attach=ref('QoSAttachStatus');
s.TelemetrySetting=closed({
 state:choices('known','unset','unsupported','unknown'),
 value:{anyOf:[integer(Number.MIN_SAFE_INTEGER,Number.MAX_SAFE_INTEGER),string(256),bool,{type:'null'}]},
});
const telemetrySettingNames = [
 'sampling','polling','header','agent','engine_type','engine_id','active_timeout','add_id_to_interface',
 'obs_domain_id','obs_point_id','cache_active_timeout','cache_max_flows','stats_interval','template_interval',
 'enable-tunnel-sampling','enable-input-sampling','enable-output-sampling',
];
s.TelemetryExporter=closed({
 state:choices('configured','unconfigured','unsupported','unknown'), reason:string(),
 native_uuid:nullable({type:'string',pattern:'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'}),
 target_count:nullable(integer(0,2147483647)), targets:nullable(array(string(256),16)),
 targets_state:choices('known','unknown','limited','not-projected'),
 settings:{type:'object',maxProperties:12,additionalProperties:false,properties:Object.fromEntries(telemetrySettingNames.map(k=>[k,ref('TelemetrySetting')]))},
});
s.TelemetryObservation=closed({
 delivery:{type:'string',const:'unverified'},
 exporters:closed({sflow:ref('TelemetryExporter'),netflow:ref('TelemetryExporter'),ipfix:ref('TelemetryExporter')}),
});
s.TelemetryObservation.description='Independent Bridge sFlow, NetFlow and IPFIX reference observations from one OVSDB snapshot. Configured describes a native reference. The bounded target and setting projection carries explicit unknown, limited, unsupported and unset cases. Collector delivery remains unverified.';
s.Resource.properties.native_telemetry=ref('TelemetryObservation');
const telemetryWriteProtocols = ['sflow', 'netflow', 'ipfix'];
const telemetryWriteSettings = {
 sflow: { sampling: nullable(integer(1,4294967295)), polling: nullable(integer(0,3600)) },
 netflow: { active_timeout: integer(-1,86400) },
 ipfix: { sampling: nullable(integer(1,4294967295)), obs_domain_id: nullable(integer(0,4294967295)), obs_point_id: nullable(integer(0,4294967295)) },
};
const telemetryTargets = minimum => ({ ...array(string(256),16,minimum), uniqueItems:true });
s.TelemetryBridgeBinding=closed({management_id:id,ovs_uuid:id,instance_generation:id,table:{type:'string',const:'Bridge'}});
const telemetryObject = ref('TelemetryBridgeBinding');
intentSchemas.TelemetryExporterSetIntent = {
 description: 'Create or update one Bridge exporter through Candidate and Safe Apply. Targets are explicit numeric unicast IPv4:UDP-port or [IPv6]:UDP-port destinations. The settings object may omit individual keys to preserve native values. Null removes an optional setting. Other native settings are preserved.',
 oneOf: telemetryWriteProtocols.map(protocol => intent('telemetry.exporter.set', {
  object:telemetryObject, protocol:{type:'string',const:protocol}, targets:telemetryTargets(1),
  settings:{type:'object',additionalProperties:false,properties:telemetryWriteSettings[protocol]},
 })),
};
intentSchemas.TelemetryExporterClearIntent = intent('telemetry.exporter.clear', {object:telemetryObject,protocol:choices(...telemetryWriteProtocols)});
intentSchemas.TelemetryExporterClearIntent.description = 'Disable one Bridge exporter through Candidate and Safe Apply. Guarded rollback restores the prior exporter configuration. Collector delivery is unverified.';
s.TelemetryExporterWriteStatus = closed({
 available:bool, reason:string(), revision:nullable(revision), configured:bool, targets:nullable(telemetryTargets(0)),
 settings:{type:'object',additionalProperties:false,properties:{
  ...telemetryWriteSettings.sflow, active_timeout:nullable(telemetryWriteSettings.netflow.active_timeout), ...telemetryWriteSettings.ipfix,
 }},
});
s.TelemetryWrite=closed({
 object:telemetryObject,
 protocols:closed(Object.fromEntries(telemetryWriteProtocols.map(protocol => [protocol,ref('TelemetryExporterWriteStatus')]))),
});
s.TelemetryWrite.description='Optional Bridge detail write status. Requires workspace.write, ovs.telemetry.write and the current observation permissions. Per-protocol availability and review revision depend on current native state and explicit Bridge write authority.';
s.Resource.properties.telemetry_write=ref('TelemetryWrite');
const statisticCounterNames = ['rx_bytes','tx_bytes','rx_packets','tx_packets','rx_errors','tx_errors','rx_dropped','tx_dropped'];
const statisticRateNames = ['rx_bytes','tx_bytes','rx_packets','tx_packets'];
const statisticCounter = nullable({type:'string',pattern:'^(0|[1-9][0-9]*)$',maxLength:20});
s.StatisticCounters=closed(Object.fromEntries(statisticCounterNames.map(key=>[key,statisticCounter])));
s.StatisticRates=closed(Object.fromEntries(statisticRateNames.map(key=>[key,nullable({type:'number',minimum:0})])));
s.StatisticSample=closed({
 sampled_at:ref('DateTime'),counters:ref('StatisticCounters'),rates:ref('StatisticRates'),
 reset:bool,discontinuity:nullable(string(128)),coalesced:bool,
});
s.Statistics=closed({
 state:choices('observed','unknown','unsupported'),reason:nullable(string(128)),
 counters:ref('StatisticCounters'),rates:ref('StatisticRates'),sampled_at:nullable(ref('DateTime')),
 reset:bool,discontinuity:nullable(string(128)),coalesced:bool,sample_count:integer(0,60),
 interval_ms:{type:'integer',const:5000},retention_seconds:{type:'integer',const:300},source:ref('Source'),
});
s.Statistics.properties.history=array(ref('StatisticSample'),60);
s.Statistics.description='Shared, bounded RAM observations of native OVS Interface counters. Counter values are exact cumulative decimal strings; null means unreported. Rates average changes in reported OVSDB values across collection intervals. sampled_at records manager observation time. Native OVS publishes periodically and its underlying datapath capture time is not established. Reset, discontinuity and skipped collection slots interrupt rate continuity. History is detail-only and disappears on manager restart.';
s.Resource.properties.statistics=ref('Statistics');
Object.assign(s.Resource.properties,{
 name:{type:'string'},interface_ref:ref('ResourceRef'),port_ref:nullable(ref('ResourceRef')),bridge_ref:nullable(ref('ResourceRef')),
 port_name:nullable({type:'string'}),bridge_name:nullable({type:'string'}),native_uuid:id,
});
s.Statistic={
 ...structuredClone(s.Resource),
 properties:{...structuredClone(s.Resource.properties),instance_generation:nullable(id)},
 required:[...s.Resource.required,'name','interface_ref','port_ref','bridge_ref','port_name','bridge_name','native_uuid'],
};
s.QoSNativeRates=closed({type:choices('linux-htb','linux-hfsc'),max_rate_bps:integer(1,4294967295),queues:array(closed({queue_id:integer(0,61439),min_rate_bps:integer(1,4294967295),max_rate_bps:integer(1,4294967295)}),32)});
s.QoSNativeBinding=closed({management_id:id,ovs_uuid:id,instance_generation:id,table:{type:'string',const:'QoS'}});
s.QoSRateChange=open({before:ref('QoSNativeRates'),after:ref('QoSNativeRates'),qos:ref('QoSNativeBinding'),bridge:object,interface:object});
s.QoSRateChange.properties.before_unbound=bool;
s.QoSRateChange.properties.after_unbound=bool;
s.ObservedIntent.properties.qos_change=ref('QoSRateChange');
s.Port.properties.qos_binding=closed({state:choices('bound','unbound','unknown','withheld','unsupported'),qos_ref:nullable(ref('ResourceRef'))});
api.info.description += ' Native Mirror sessions retain source/destination direction, independent VLAN selection, a reserved output Port or RSPAN VLAN and optional snap length. Shared Safe Apply requires independent Mirror policy, exact native graph/reference checks and fresh deletion recovery identities. Statistics remain observations and private advanced filter expressions are not exposed or adopted.';
Object.assign(s, intentSchemas);
s.QoSIntent.description='Update explicit rates on one existing, independently authorized Port and dedicated native QoS and Queue mapping. The current scheduler type and all explicit Queue numbers must match the requested layout. OVSDB and Linux evidence are checked independently during protected execution and rollback.';
s.QoSDetachIntent.description='Detach a dedicated linear HFSC QoS from a reviewed veth Port through durable Safe Apply. Preserves the existing QoS and Queue rows. Requires exact Port and QoS identities, an independent detachment grant, shared execution authorization, and fresh Linux proof of scheduler removal or restoration.';
s.QoSAttachIntent.description='Attach an unbound dedicated linear HFSC QoS to an eligible Linux veth Port through durable Safe Apply. Requires exact Port and QoS identities, an independent attachment grant, and Linux proof of the installed profile or restoration of the unshaped default.';
s.QoSCreateIntent.description='Create one unbound linear HFSC QoS and its dedicated native Queues through Candidate and durable Safe Apply. Requires an independently authorized unique profile name and the current provider generation. Rates use positive multiples of 8 bit/s. Queue minimum cannot exceed its maximum or the QoS maximum. With 32 explicit Queues, include Queue 0. Safe Apply verifies the saved configuration and detached graph. Later Port attachment is a separately authorized change.';
s.SpanningTreePrepareRequest = closed({protocol:choices('disabled','stp','rstp'),parameters:{type:'object',additionalProperties:false,properties:Object.fromEntries(['stp-priority','stp-hello-time','stp-max-age','stp-forward-delay','rstp-priority','rstp-max-age','rstp-forward-delay'].map(k=>[k,nullable({type:'string',minLength:1,maxLength:128,pattern:'^[0-9]+$'})]))}});
s.SpanningTreePrepareIntent.description = 'Draft preparation only, requiring configuration.read, workspace.write, ovs.bridge.spanning-tree.prepare and independent root-owned immutable Bridge authority. Current configuration read permission is required for current and historical review. Missing parameter keys preserve the captured original; null removes an active protocol map key. Disabled changes preserve every parameter. Inactive settings are preserved and validated. Strict native/basic scope, full private graph digests and sealed originals guard review. Rebase cannot replace originals. Validation always retains SPANNING_TREE_EXECUTION_PENDING; no Apply or Safe Apply execution adapter accepts this intent. The legacy spanning_tree.configure intent remains unsupported.';
s.SpanningTreeNativeConfig=closed({stp_enable:bool,rstp_enable:bool,parameters:closed(Object.fromEntries(['stp-priority','stp-hello-time','stp-max-age','stp-forward-delay','rstp-priority','rstp-max-age','rstp-forward-delay'].map(k=>[k,nullable({type:'string',maxLength:128})])))});
s.SpanningTreeDraft=closed({before:ref('SpanningTreeNativeConfig'),after:ref('SpanningTreeNativeConfig')});
s.ObservedIntent.properties.spanning_tree_change=ref('SpanningTreeDraft');
s.SpanningTreePreparation=closed({available:bool,reason:{type:'string',maxLength:128},authority:string(),mode:{type:'string',const:'draft-only'},capability:{type:'string',const:'ovs.bridge.spanning-tree.prepare'}});
s.SpanningTree.properties.preparation=ref('SpanningTreePreparation');
s.SpanningTreeConfigureIntent.description = 'Independent guarded STP/RSTP execution intent. Requires an explicitly assigned ovs.bridge.spanning-tree.write capability, separate root Bridge preparation and execution grants, an exact qualified optional OVS build, durable graph reservation and Safe Apply. Complete native before-image waits, repeated current authorization and installed local/exclusive protocol witnesses guard dispatch and Applied. Protocol transitions interrupt traffic; no network convergence guarantee. Native setter order must qualify forward and compensation. Missing keys preserve originals and null removes an active protocol key. Legacy and prepare intents remain non-executable.';
s.SpanningTreeExecution=closed({available:bool,reason:{type:'string',maxLength:128},capability:{type:'string',const:'ovs.bridge.spanning-tree.write'},mode:{type:'string',const:'safe-apply'}});
s.SpanningTree.properties.execution=ref('SpanningTreeExecution');
s.InternalPortCreateIntent.description = 'Create one fresh internal access Port and Interface under an existing system Bridge binding. Requires ovs.port.internal.create and a root-owned Bridge management-id:name grant. No physical adoption, local-port change, host IP configuration or direct live write. Safe Apply rollback removes only unchanged created children and preserves the captured parent membership.';
s.InternalPortCreation = open({ name: string(15), vlan_id: integer(1,4094), root_uuid: id, bridge: object, bridge_name: string(15), interface: object, original_members: array(object,32,1), local_port: object, local_interface: object, before_present: bool, after_present: bool });
s.ObservedIntent.properties.internal_port_creation = ref('InternalPortCreation');
s.InternalPortDeleteIntent.description = 'Delete one manager-owned independent internal access Port and its Interface, preserving the existing Bridge and other members. Requires ovs.port.internal.delete and an independent root Bridge management-id:name deletion grant. Compensation reserves fresh child identities. Changed native configuration, references or host dependencies block execution. Input cannot provide captured graphs, markers or replacement identities.';
s.InternalPortGraph = open({ port: ref('ObservedBinding'), configuration: ref('InternalPortCreation') });
s.InternalPortDeletion = open({ source: ref('InternalPortGraph'), replacement: ref('InternalPortGraph') });
s.ObservedIntent.properties.internal_port_deletion = ref('InternalPortDeletion');
s.BondIntent.properties.fallback = fallback;
s.BondIntent.description = 'Existing native Bond-as-Port fields only. member_interface_ids must equal the current immutable member bindings; membership changes remain gated. An omitted fallback preserves the captured native value.';
s.NativeBond = open({ lacp: nullable({ type: 'string' }), bond_mode: nullable({ type: 'string' }), lacp_fallback_ab: nullable({ type: 'string' }) });
Object.assign(s.ObservedIntent.properties, { bond: ref('NativeBond'), before_bond: ref('NativeBond'), bond_member_interface_ids: { type: 'array', items: id } });
s.BridgeCreation = open({ name: string(15), root_uuid: id, local_port: object, local_interface: object, before_present: bool, after_present: bool });
s.ObservedIntent.properties.bridge_creation = ref('BridgeCreation');
s.IsolatedBridgeIntent.description = 'Create one fresh isolated system Bridge with its local Port and internal Interface. Manager assigns all identities. Requires a reviewed root name allowlist and ovs.bridge.create. Existing names, client identities, members and mixed-intent batches are rejected. Creation can only run through Safe Apply; rollback removes only the unchanged created graph.';
s.BridgeGraph = open({ name: string(15), root_uuid: id, bridge: ref('ObservedBinding'), local_port: ref('ObservedBinding'), local_interface: ref('ObservedBinding') });
s.BridgeDeletion = open({ source: ref('BridgeGraph'), replacement: ref('BridgeGraph') });
s.BridgeDeletion.description = 'Captured original and proposed fresh compensation identities are review evidence, not writable input or proof of restoration.';
s.IsolatedBridgeDeleteIntent.description = 'Delete one unchanged isolated Bridge graph previously created by this manager. Requires the independent root deletion name gate and ovs.bridge.delete. Safe Apply compensation recreates the graph with fresh identities; deleted identities are never reused. Input cannot supply graphs, markers or replacement identities.';
s.ObservedIntent.properties.bridge_deletion = ref('BridgeDeletion');
s.IdentityReplacement = open({ previous: ref('ObservedBinding'), replacement: ref('ObservedBinding'), state: { type: 'string', 'x-known-values': ['reserved', 'restored', 'not-used', 'unverified'] } });
s.IdentityReplacement.description = 'Only restored proves settled compensation with Applied and health evidence. Other or unknown states do not establish a usable replacement.';
s.Transaction.properties.identity_replacements = array(ref('IdentityReplacement'), 32);
s.ObservedIntent.properties.operation['x-known-values'] = ['port.vlan.set', 'bond.configure', 'port.lacp.set', 'bridge.create-isolated', 'bridge.delete-isolated', 'port.create-internal', 'port.delete-internal'];
s.ObservedIntent.properties.operation['x-known-values'].push(...topologyOperations);
s.ObservedIntent.description = 'The operation selects its typed field group. VLAN uses value/before; Bond and LACP use bond/before_bond; Interface MTU uses mtu_change. Unknown native values are preserved and never silently normalized.';
s.InterfaceMTUIntent.description = 'Set an explicit mtu_request on one independently authorized standalone non-local internal Interface. Empty original requests require sealed, proven Bridge MTU dependencies. Single intent only; actual MTU proof and exact original explicit or empty request restoration are required. No type/options mutation, Bond member, local Interface or physical adoption.';
s.InterfaceMTUClearIntent.description = 'Clear an existing explicit request to the native empty optional set. Requires the same independent root Interface authority and ovs.interface.mtu.write. Captured Bridge MTU contributors, parent relations and actual MTU proof guard execution, confirmation and compensation. No guessed 1500 default or unknown-result write replay.';
s.MTUChange = open({ before: nullable(integer(576,65535)), after: nullable(integer(576,65535)), port: object, bridge: object });
s.MTUDefaultContext = open({mtu: integer(576,65535), dependency: revision, bindings: array(object,64,1)});
s.MTUChange.properties.default_context = ref('MTUDefaultContext');
s.ObservedIntent.properties.mtu_change = ref('MTUChange');
s.ObservedIntent.properties.operation['x-known-values'].push('interface.mtu.set', 'interface.mtu.clear');
s.PolicingRequest = { oneOf: [
 closed({mode: {type:'string', const:'disabled'}, rate: {type:'integer', const:0}}),
 closed({mode: {type:'string', const:'bandwidth'}, rate: integer(1,1000000)}),
 closed({mode: {type:'string', const:'packets'}, rate: integer(1,1000)}),
] };
s.NativePolicing = open({ingress_policing_rate:integer(0,1000000), ingress_policing_burst:integer(0,2147483647), ingress_policing_kpkts_rate:integer(0,1000), ingress_policing_kpkts_burst:integer(0,2147483647)});
s.PolicingChange = open({before:ref('NativePolicing'), after:ref('NativePolicing'), port:object, bridge:object, name:string(15), ifindex:integer(1,2147483647)});
s.ObservedIntent.properties.policing_change = ref('PolicingChange');
s.ObservedIntent.properties.operation['x-known-values'].push('interface.policing.set');
s.ObservedIntent.properties.operation['x-known-values'].push('qos.configure', 'qos.detach', 'qos.attach', 'qos.create');
s.ObservedIntent.properties.operation['x-known-values'].push('telemetry.exporter.set', 'telemetry.exporter.clear');
s.InterfacePolicingIntent.description = 'Single controlled Interface ingress policing intent. Bandwidth rate is kbit/s; packet rate is kpps. Disabled requires zero. Both native bursts must remain zero (OVS-selected defaults). Requires ovs.interface.policing.write and a root exclusive-ingress Interface identity grant, fresh standalone non-local internal system graph, no raw options and software-only supported rules. Captured parents, all four configuration values and private kernel before-image guard dispatch, Applied, confirmation and exact compensation. No client-supplied before-image, default, host path, owner, raw transaction, compensation or result evidence.';
s.Intent = { oneOf: [ref('VlanIntent'), ...Object.keys(intentSchemas).map(ref)] };
s.CandidateCommand.oneOf[0].properties.intents.items = ref('Intent');
s.ProfileCommand = command({ name: string(), description: { type: 'string', maxLength: 4096 }, intents: array(ref('Intent'), 32) });
s.LabelCommand = command({ name: string(), description: { type: 'string', maxLength: 4096 } });
s.UserCreateCommand = command({ username: string(), role_ids: array(id, 64), password: { ...string(1024), writeOnly: true, 'x-sensitive': true } });
s.UserUpdateCommand = command({ disabled: bool, role_ids: array(id, 64) });
s.PasswordCommand = command({ password: { ...string(1024), writeOnly: true, 'x-sensitive': true } });
s.RoleCommand = command({ name: string(), capabilities: array(string(), 128) });
s.TokenCommand = command({ name: string(), scopes: array(string(), 128), expires_at: ref('DateTime') });
s.EmptyCommand = command({});
s.Reauthentication = closed({ password: { ...string(1024), writeOnly: true, 'x-sensitive': true } });
s.AAACommand = command({ servers: array(closed({ host: string(256), port: integer(1, 65535), secret_ref: id, timeout_seconds: integer(1, 10) }), 8), local_fallback: bool });
s.CertificateCommand = command({ certificate_pem: string(65536), private_key_pem: { ...string(65536), writeOnly: true, 'x-sensitive': true } });
s.SettingsCommand = command({ locale: choices('zh-CN', 'en-US'), theme: choices('light', 'dark', 'system'), view_mode: choices('standard', 'expert') });
s.DiagnosticCommand = command({ template_id: string(64), target_ref: ref('ResourceRef'), duration_seconds: integer(1, 300), options: closed({ count: integer(1, 10), timeout_ms: integer(100, 5000) }) });
s.CaptureCommand = command({ interface_id: id, duration_seconds: integer(1, 300), max_bytes: integer(1, 67108864), filter: { type: 'string', maxLength: 2048 } });
s.SupportCommand = command({ include_kinds: array(choices('runtime', 'ovs', 'health', 'events', 'audit'), 5), redact: { type: 'boolean', const: true } });
s.LifecycleCommand = command({ operation: choices('start', 'stop', 'restart', 'reload'), preflight_id: id, expected_generation: id, reason: string(1024) });
s.RestoreCommand = command({ backup_id: id, verification_id: id, expected_generation: id, reason: string(1024) });
s.ImportCommand = command({ artifact_id: id, expected_generation: id, mode: { type: 'string', const: 'candidate' } });
s.ArtifactCommand = command({ upload_id: id, part: integer(0, 65535), data_base64: { ...string(65536), pattern: '^[A-Za-z0-9+/]*={0,2}$' }, final: bool });
s.OpenFlowCommand = command({ object, validation_id: id, expected_revision: revision });
s.OpenFlowPreflight = command({ object, operation: choices('flow.add', 'flow.delete', 'group.add', 'group.delete', 'meter.add', 'meter.delete'), specification_artifact_id: id });

const param = (name, location, schema, required = false) => ({ name, in: location, required, schema });
const pagination = [param('limit', 'query', { ...integer(1, 500), default: 100 }), param('cursor', 'query', string(4096)), param('filter', 'query', string(1024))];
const problems = Object.fromEntries([400, 401, 403, 404, 405, 409, 410, 412, 413, 415, 422, 428, 429, 500, 503].map(code => [code, { description: 'Structured error; command_effect does not prove Not Applied.', content: { 'application/problem+json': { schema: ref('Problem') } } }]));
export function add(path, method, operationId, response, capability, issue, options = {}) {
 const parameters = [...path.matchAll(/\{([^}]+)\}/g)].map(m => param(m[1], 'path', id, true));
 if (options.page) parameters.push(...structuredClone(pagination));
 if (options.query) parameters.push(...options.query);
 if (options.domain) parameters.push(param('Idempotency-Key', 'header', ref('RequestId'), true), param('X-OVS-Request-Epoch', 'header', id, true), param('X-OVS-CSRF-Token', 'header', string(256)));
 if (options.etag) parameters.push(param('If-Match', 'header', string(132), true));
 const status = options.status ?? (options.domain ? 202 : 200);
 const op = { operationId, summary: operationId, 'x-capability': capability, 'x-service-issue': issue, 'x-service-state': options.public ? 'transport' : 'service-pending', 'x-resource-kind': options.kind ?? parameters.find(p => p.in === 'path')?.name.replace(/_id$/, '') ?? '', parameters, responses: { ...structuredClone(problems), [status]: { description: status === 202 ? 'Durable receipt and Job; retrieve REST resources to resolve outcome.' : 'Authoritative representation.', content: { 'application/json': { schema: ref(response) } } } } };
 if (options.domain) op['x-request-domain'] = options.domain;
 if (options.body) op.requestBody = { required: true, content: { 'application/json': { schema: ref(options.body) } } };
 if (options.sensitive) op['x-sensitive-request'] = true;
 if (options.secretResponse) op['x-secret-response'] = true;
 if (options.public) op.security = [];
 if (options.conditions) op['x-conditions'] = options.conditions;
 (api.paths[path] ??= {})[method] = op;
 return operationId;
}

// Retain the reviewed first slice and make its transport policy executable.
const policies = {
 createSession: ['auth.login', 34], readSession: ['session.read', 34], deleteSession: ['session.end', 34], readWorkspace: ['workspace.read', 38],
 listPorts: ['state.read', 36], readPort: ['state.read', 36], readCandidate: ['workspace.read', 38], changeCandidate: ['config.stage', 38],
 createValidation: ['config.validate', 38], readValidation: ['config.validate', 38], createTransaction: ['config.apply', 39], readTransaction: ['config.read', 39],
 decideTransaction: ['config.decide', 40], reconcileTransaction: ['config.read', 39], readRequestReceipt: ['request.read', 33], readJob: ['job.read', 37], listEvents: ['event.read', 37], listAudit: ['audit.read', 37], subscribeResourceChanges: ['state.read', 33],
};
for (const methods of Object.values(api.paths)) for (const op of Object.values(methods)) {
 const [capability, issue] = policies[op.operationId];
 const kind = ({ listPorts: 'port', readPort: 'port', readCandidate: 'candidate', changeCandidate: 'candidate', readJob: 'job', readTransaction: 'transaction', decideTransaction: 'transaction', reconcileTransaction: 'transaction', readRequestReceipt: 'request', readValidation: 'validation', listEvents: 'event', listAudit: 'audit' })[op.operationId] ?? '';
 Object.assign(op, { 'x-capability': capability, 'x-service-issue': issue, 'x-service-state': 'service-pending', 'x-resource-kind': kind });
 Object.assign(op.responses, structuredClone(problems));
}
api.paths['/sessions'].post['x-sensitive-request'] = true;
api.paths['/session'].delete.parameters.find(p => p.name === 'X-OVS-CSRF-Token').required = false;
s.RequestReceipt.properties.state['x-known-values'].push('completed');
api.paths['/requests/{request_id}'].get.parameters.find(p => p.name === 'epoch').required = true;
api.paths['/requests/{request_id}'].get.parameters.find(p => p.name === 'epoch').description = 'Original epoch, required for exact read-only recovery even after restore.';
api.paths['/stream'].get['x-service-state'] = 'transport';
add('/contract', 'get', 'readContract', 'ContractInfo', '', 33, { public: true });
add('/runtime', 'get', 'readRuntime', 'Runtime', '', 31, { public: true });
add('/inventory', 'get', 'readInventory', 'Resource', 'inventory.read', 36);
add('/inventory/topology', 'get', 'readInventoryTopology', 'Resource', 'inventory.read', 42);
api.paths['/inventory/topology'].get.parameters.push(param('domain', 'query', choices('mirror')));
api.paths['/inventory/topology'].get.description='Bounded native topology review. Optional domain=mirror limits the public selector to locally provisioned Mirror creation Bridges and their Port/Interface/Mirror graph; server validation and native CAS retain the complete private dependency graph. Truncation closes creation.';
add('/inventory/schema', 'get', 'readInventorySchema', 'ResourcePage', 'inventory.read', 36, { page: true });
const evidenceFilters = [param('correlation_id', 'query', id), param('object_id', 'query', id), param('job_id', 'query', id), param('origin', 'query', choices('Manager', 'External', 'Unknown'))];
for (const [path, kind, capability] of [['events', 'event', 'event.read'], ['audit', 'audit', 'audit.read']]) {
 const list = api.paths[`/${path}`].get;
 list.parameters.push(param('filter', 'query', string(1024)), ...structuredClone(evidenceFilters.filter(p => p.name !== 'correlation_id')));
 add(`/${path}/{${kind}_id}`, 'get', `read${nameOfEvidence(kind)}`, 'Resource', capability, 37, { kind });
 add(`/${path}/export`, 'get', `export${nameOfEvidence(path)}`, 'ResourcePage', capability, 37, { kind, page: true, query: evidenceFilters });
}
function nameOfEvidence(value) { return value[0].toUpperCase() + value.slice(1); }
add('/jobs/export', 'get', 'exportJobs', 'JobPage', 'job.read', 37, { kind: 'job', page: true, query: evidenceFilters });
add('/session/reauthentication', 'post', 'reauthenticateSession', 'Session', 'session.read', 34, { body: 'Reauthentication', status: 200, sensitive: true });
// OpenAPI itself is a document, not a management resource.
api.paths['/openapi.json'] = { get: { operationId: 'readOpenAPI', security: [], 'x-capability': '', 'x-service-issue': 33, 'x-service-state': 'transport', parameters: [], responses: { 200: { description: 'OpenAPI 3.1.1 document', content: { 'application/json': { schema: { type: 'object', additionalProperties: true } } } } } } };

Object.assign(s.Resource.properties, {
 address: string(45), mac: nullable(string(17)), family: choices('ipv4','ipv6'), ifindex: integer(1,2147483647),
 interface_name: string(15),
});
s.Neighbor = structuredClone(s.Resource);
s.NeighborCoverage = closed({
 scope: choices('managed-linux-interfaces'), eligible_interfaces: integer(0,2048), observed_interfaces: integer(0,2048),
 matched_entries: integer(0,2048), returned_entries: integer(0,50), max_entries: integer(2048,2048),
});
s.LldpIdentifier = closed({subtype: integer(1,7), encoding: choices('text','hex','mac'), value: string(510)});
s.LldpAdvertisement = closed({
 chassis_id: ref('LldpIdentifier'), port_id: ref('LldpIdentifier'), system_name: nullable(string(255)),
 ttl_seconds: integer(1,65535), last_seen_at: ref('DateTime'),
});
s.Resource.properties.lldp = ref('LldpAdvertisement');
s.LldpCoverage = closed({
 scope: choices('opted-in-managed-interfaces'), configured_interfaces: integer(0,16), observed_interfaces: integer(0,16),
 matched_entries: integer(0,256), returned_entries: integer(0,50), max_entries: integer(256,256),
});
const fdbMAC = {...string(17), minLength:17, pattern:'^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$|^([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}$'};
s.FdbObservation = {
 type:'object', additionalProperties:true,
 properties:{
  mac:fdbMAC, vlan:integer(0,4095), bridge_name:string(15), bridge_ref:ref('ResourceRef'),
  port_resolution:choices('resolved','unmapped','ambiguous'), port_name:string(64), ofport:integer(1,65534),
  port_ref:ref('ResourceRef'), age_seconds:integer(0,4294967295), last_seen_at:ref('DateTime'),
 },
 required:['mac','vlan','bridge_name','bridge_ref','port_resolution'],
 description:'A sampled native FDB row. The OpenFlow number may be the representative member of a Bond. A Port reference requires an exact logical Port association. Last seen is an estimate derived from the sampled whole-second age. Static rows omit both age and last seen.',
};
s.FdbCoverage = closed({
 bridges_sampled:integer(0,8), bridges_total:integer(0,2048), entries_sampled:integer(0,2048),
 entries_total:integer(0,34359738360), truncated:bool,
});
s.FdbCoverage.description = 'Bridge coverage applies after Bridge/Port scope selection. Entry counts describe all learned and static rows from sampled Bridges before MAC, VLAN, Port or free-text row filters. The total is the native count across sampled Bridges only.';
s.Resource.properties.fdb = ref('FdbObservation');

const endpointBasis = choices('fdb','neighbor','lldp');
const endpointIP = {...string(45), description:'IPv4 or IPv6 address without an interface zone.'};
const endpointAvailability = choices('complete','degraded','unavailable');
s.EndpointEvidence = {
 type:'object', additionalProperties:true,
 properties:{
  ref:ref('ResourceRef'), source:ref('Source'), state:string(),
  ip:endpointIP, family:choices('ipv4','ipv6'), mac:fdbMAC, vlan:integer(0,4095),
  ofport:integer(1,65534), port_name:string(64), port_resolution:choices('resolved','unmapped','ambiguous'),
  chassis_id:ref('LldpIdentifier'), lldp_port_id:ref('LldpIdentifier'), system_name:string(255),
  ttl_seconds:integer(1,65535), last_seen_at:ref('DateTime'),
 },
 required:['ref','source','state'],
 description:'One retained evidence row and its source, using the exact resource reference of that observation. FDB last seen is an estimate from native age. LLDP last seen is the receive time of an advertisement.',
};
s.EndpointObservation = {
 type:'object', additionalProperties:true,
 properties:{
  basis:endpointBasis, mac:fdbMAC, vlan:integer(0,4095), bridge_ref:ref('ResourceRef'), bridge_name:string(256),
  port_ref:ref('ResourceRef'), port_name:string(256), interface_ref:ref('ResourceRef'), interface_name:string(15),
  addresses:array(endpointIP,12), evidence:array(ref('EndpointEvidence'),12,1),
  evidence_total:integer(1,2048), evidence_truncated:bool,
 },
 required:['basis','addresses','evidence','evidence_total','evidence_truncated'],
 description:'Scoped endpoint evidence. FDB, Linux ARP/NDP and LLDP retain independent evidence and source authority. ARP/NDP may group reported usable MAC bindings on the very same managed local Interface. A matching value across protocols never establishes identity or VLAN association. Addresses describe the evidence shown.',
};
s.EndpointSourceCoverage = {
 type:'object', additionalProperties:true,
 properties:{
  kind:endpointBasis, source:ref('Source'), availability:endpointAvailability, reason:string(128),
  scope_reason:choices('VLAN_NOT_REPORTED','IP_NOT_REPORTED','MAC_NOT_VERIFIED'),
  scope_unit:choices('bridge','interface'), eligible:integer(0,2048), observed:integer(0,2048), truncated:bool,
 },
 required:['kind','source','availability','scope_unit','eligible','observed','truncated'],
 description:'Independent provider freshness and coverage for the selected topology. scope_reason means this provider cannot establish the requested exact row filter. Coverage counts are prior to row filtering.',
};
s.Resource.properties.endpoint = ref('EndpointObservation');

const resources = [
 ['bridges','bridge','Bridge',36], ['interfaces','interface','Interface',36], ['vlans','vlan','Resource',42], ['bonds','bond','Bond',42],
 ['spanning-tree','spanning_tree','Resource',43], ['multicast','multicast','Resource',43], ['tunnels','tunnel','Resource',43], ['mirrors','mirror','Resource',43],
 ['qos','qos','Resource',43], ['isolation','isolation','Resource',43], ['openflow/flows','flow','Resource',47], ['openflow/groups','group','Resource',47],
 ['openflow/meters','meter','Resource',47], ['openflow/controllers','controller','Resource',47], ['endpoints','endpoint','Resource',44], ['fdb','fdb_entry','Resource',44],
 ['neighbors','neighbor','Resource',44], ['lldp','lldp_neighbor','Resource',44], ['statistics','statistic','Resource',44], ['dpdk','dpdk','Resource',47],
 ['offload','offload','Resource',47], ['telemetry','telemetry','Resource',44], ['capabilities','capability','Resource',45], ['drift','drift','Resource',43],
 ['checkpoints','checkpoint','Resource',48], ['diagnostics/templates','diagnostic_template','Resource',46], ['jobs','job','Job',37], ['transactions','transaction','Transaction',39], ['backups','backup','Resource',48],
 ['certificates','certificate','Resource',35], ['profiles','profile','Metadata',43], ['labels','label','Metadata',43], ['users','user','User',34], ['roles','role','Role',34], ['tokens','token','Token',34],
];
const name = value => value.split(/[-_/]/).map(x => x[0].toUpperCase() + x.slice(1)).join('');
for (const [path, kind, schema, issue] of resources) {
 const page = name(kind) + 'Page';
 s[page] = { ...structuredClone(s.ResourcePage), properties: { ...structuredClone(s.ResourcePage.properties), items: array(ref(schema), 500) } };
 const cap = ({user:'user.read',role:'role.read',token:'token.read',certificate:'certificate.read',backup:'backup.read',checkpoint:'recovery.read',job:'job.read',transaction:'config.read',profile:'workspace.read',label:'workspace.read',diagnostic_template:'diagnostic.read'})[kind] ?? 'state.read';
 add(`/${path}`, 'get', `list${name(path)}`, page, cap, issue, { page: true, kind });
 if (!api.paths[`/${path}/{${kind}_id}`]) add(`/${path}/{${kind}_id}`, 'get', `read${name(kind)}`, schema, cap, issue, { kind });
}
Object.assign(s.InterfacePage.properties, { source: ref('Source'), availability: string(), reason: nullable({ type: 'string' }), coverage: { type: 'object', additionalProperties: true } });
Object.assign(s.NeighborPage.properties, {source: ref('Source'), availability: string(), reason: nullable(string(128)), coverage: ref('NeighborCoverage')});
Object.assign(s.EndpointPage.properties, {
 availability:endpointAvailability, freshness:choices('fresh','stale','unavailable'),
 matched:integer(0,4352), sources:array(ref('EndpointSourceCoverage'),3,3),
});
api.paths['/endpoints'].get.parameters.push(param('mac','query',fdbMAC),param('ip','query',endpointIP),param('vlan','query',integer(0,4095)),param('bridge','query',id),param('port','query',id));
api.paths['/endpoints/{endpoint_id}'].get.parameters.push(param('ip','query',endpointIP));
for(const path of ['/endpoints','/endpoints/{endpoint_id}']){
 api.paths[path].get['x-service-state']='implemented';
 api.paths[path].get.description='Read scoped FDB, ARP/NDP and LLDP evidence from shared observation caches. Requires inventory.read and state.read. Source gaps and provenance remain explicit. Browser reads do not invoke native collectors.';
}
api.paths['/endpoints'].get.description += ' Exact Bridge, Port, VLAN, MAC and IP filters require independent source evidence. Unqualified sources are excluded with explicit coverage explanations. Cursors bind filters and the current observation set. At most 50 rows per page and two evidence records per row.';
api.paths['/endpoints/{endpoint_id}'].get.description += ' At most 12 evidence records. The optional IP parameter prioritizes matching address evidence within this bounded preview. Missing identities under incomplete coverage return 503.';
api.info.description += ' Endpoints combines the view of FDB, ARP/NDP and LLDP while preserving independent scope, provenance and coverage.';
Object.assign(s.FdbEntryPage.properties, {
 source:choices('ovs_fdb'), availability:choices('available','unavailable','degraded'), freshness:choices('fresh','stale','unavailable'),
 reason:choices('disabled','unsupported','not_ready','timeout','permission','source_unavailable','source_invalid','no_managed_bridges','partial','truncated','expired','epoch_changed'),
 coverage:ref('FdbCoverage'), observed_at:ref('DateTime'),
});
api.paths['/fdb'].get.parameters.push(param('mac','query',fdbMAC),param('vlan','query',integer(0,4095)),param('bridge','query',id),param('port','query',id));
for(const path of ['/fdb','/fdb/{fdb_entry_id}']){
 api.paths[path].get['x-service-state']='implemented';
 api.paths[path].get.description='Read native OVS FDB observations. Requires state.read and inventory.read and an independent, root-reviewed bounded FDB witness configuration. No write, Linux neighbor inference, or OVS command on request.';
}
api.paths['/fdb'].get.description += ' Sample at most eight enabled Bridges and keep at most 256 entries per Bridge. Pages contain up to 50 sampled rows. Filters and cursors apply to a single immutable sample. observed_at is the oldest included Bridge sample; individual rows preserve their own sample time. Truncated, unavailable, stale and partial coverage remain explicit.';
api.info.description += ' Native FDB observation is opt-in with a qualified bounded OVS witness. Filters, paging and bounded export operate on cached observations.';
Object.assign(s.LldpNeighborPage.properties, {source: ref('Source'), availability: string(), reason: nullable(string(128)), coverage: ref('LldpCoverage')});
api.paths['/lldp'].get.parameters.push(...['bridge_id','port_id','interface_id'].map(key=>param(key,'query',id)));
for(const path of ['/lldp','/lldp/{lldp_neighbor_id}']){
 api.paths[path].get['x-service-state']='implemented';
 api.paths[path].get.description='Read LLDP advertisements received on explicitly opted-in managed Linux Interfaces. Requires state.read and inventory.read. Source time is the latest collection; last_seen_at is the kernel receive time. Rows expire at the advertised TTL. This observation does not attest to remote identity or forwarding.';
}
api.paths['/lldp'].get.description += ' Disabled or stale capture is explicit. Up to 256 active rows and 50 items per page; truncated denotes capture or retention loss. Pagination and filters apply to the same authorized result set.';
api.paths['/neighbors'].get.parameters.push(param('family','query',choices('ipv4','ipv6')),...['bridge_id','port_id','interface_id'].map(key=>param(key,'query',id)));
for(const path of ['/neighbors','/neighbors/{neighbor_id}']){
 api.paths[path].get['x-service-state']='implemented';
 api.paths[path].get.description='Read IPv4 ARP and IPv6 NDP entries from a shared Linux rtnetlink observation. Requires state.read and inventory.read. Only individually verified associations to active managed Interfaces are covered. Observed time is the manager sample time. Kernel NUD state does not attest to reachability beyond the recorded observation.';
}
api.paths['/neighbors'].get.description += ' Visible rows are limited to 2048 per sample and 50 per page. next_cursor continues the current result set; truncated reports limited collector retention. Address, MAC and Interface name accept substring filtering. Missing or stale coverage is explicit and withholds rows.';
Object.assign(s.TelemetryPage.properties, { source: ref('Source'), availability: string(), reason: nullable({ type: 'string' }), coverage: { type: 'object', additionalProperties: true } });
s.QosPage.properties.qos_create=ref('QoSCreateStatus');
s.SpanningTreeObservationPage = open({ ...s.ResourcePage.properties, items: array(ref('SpanningTree'),500), source: ref('Source'), availability: string(), reason: nullable({type:'string'}), coverage: {type:'object',additionalProperties:true} });
Object.assign(s.StatisticPage.properties,{source:ref('Source'),availability:string(),reason:nullable(string(128)),coverage:{type:'object',additionalProperties:true}});
api.paths['/statistics'].get.parameters.push(...['bridge_id','port_id','interface_id'].map(key=>param(key,'query',id)));
for(const path of ['/statistics','/statistics/{statistic_id}']){
 api.paths[path].get['x-service-state']='implemented';
 api.paths[path].get.description='Read shared RAM traffic observations for exact Interface identities. Requires state.read and inventory.read. Port or Bridge scope returns individual member Interfaces. Source times are manager observations of periodically published OVSDB counters. Short retained history is bounded, volatile and detail-only.';
}
api.paths['/statistics'].get.description += ' The optional limit defaults to 100 and accepts values from 1 to 500. Each returned page is bounded by 50 Interfaces or 40 KiB, with next_cursor for additional selected identities. Members retain independent observation timestamps.';
s.Port.properties.spanning_tree = ref('SpanningTreePortObservation');
s.Bond.properties.spanning_tree = ref('SpanningTreePortObservation');
api.paths['/ports'].get.parameters.push(param('filter', 'query', string(1024)));
add('/inventory/spanning-tree', 'get', 'listSpanningTreeObservations', 'SpanningTreeObservationPage', 'state.read', 43, {page:true, kind:'bridge'});
add('/bridges/{bridge_id}/spanning-tree', 'get', 'readSpanningTreeObservation', 'SpanningTree', 'state.read', 43, {kind:'bridge'});
api.paths['/inventory/spanning-tree'].get['x-service-state'] = 'implemented';
api.paths['/bridges/{bridge_id}/spanning-tree'].get['x-service-state'] = 'implemented';
for (const path of ['/tunnels','/tunnels/{tunnel_id}']) api.paths[path].get['x-service-state'] = 'implemented';
for (const path of ['/mirrors','/mirrors/{mirror_id}']) api.paths[path].get['x-service-state'] = 'implemented';
for (const path of ['/telemetry','/telemetry/{telemetry_id}']) {
 api.paths[path].get['x-service-state'] = 'implemented';
 api.paths[path].get.description = 'Read-only OVSDB observation of sFlow, NetFlow and IPFIX attached to each Bridge. The telemetry ID is the active Bridge management identity. Requires inventory.read and configuration.read in addition to state.read. IPFIX covers Bridge sampling. Collector delivery remains unverified.';
}
api.paths['/telemetry'].get.description += ' The bounded list includes independently observed exporter states and target counts.';
api.paths['/telemetry/{telemetry_id}'].get.description += ' The detail includes up to 16 configured collector targets per exporter and the supported native settings. Eligible sessions can inspect telemetry_write for Bridge exporter changes through Candidate and Safe Apply.';
for (const path of ['/qos','/qos/{qos_id}']) {
 api.paths[path].get['x-service-state'] = 'implemented';
 api.paths[path].get.description = 'Read-only QoS configuration from one OVSDB snapshot. Requires inventory.read and configuration.read in addition to state.read. Queue numbers are scoped to a QoS record. Observed configuration does not establish scheduler installation, queue selection or traffic shaping.';
}
api.paths['/qos/{qos_id}'].get.description += ' The detail response also samples the associated Linux root scheduler and supported HTB/HFSC Queue classes when the current Port and Interface identities can be verified.';
api.paths['/interfaces'].get.parameters.push(
 param('bridge_id', 'query', id),
 param('native_type', 'query', { type: 'string', maxLength: 64 }),
 param('link_state', 'query', choices('up', 'down', 'unknown')),
);
api.paths['/interfaces'].get.description = 'Filters apply to the entire confirmed snapshot before pagination. bridge_id is an active immutable Bridge management identity; a retired or absent identity returns BRIDGE_SCOPE_NOT_FOUND. native_type is exact and requires configuration.read, including when empty (native system default); unknown is never inferred as a default. link_state is the OVS observation, with unknown covering empty, missing or unrecognized samples of a monitored column; no Linux carrier inference. Names use case-insensitive ASCII digit-run natural order, then exact name and management identity. Cursors bind every selection, order, principal, permission revision, generation and snapshot; changing selection requires the first page.';

api.paths['/jobs'].get.parameters.push(...structuredClone(evidenceFilters));
for (const [path, op, issue, cap] of [['health','readHealth',45,'state.read'],['management-network','readManagementNetwork',49,'state.read'],['ovs-lifecycle','readOVSLifecycle',49,'state.read'],['aaa','readAAA',34,'access.read'],['settings','readSettings',54,'workspace.read'],['about','readAbout',55,'state.read'],['topology','readTopology',52,'state.read']]) add(`/${path}`, 'get', op, 'Resource', cap, issue);
add('/search','get','searchResources','ResourcePage','state.read',52,{page:true,query:[param('q','query',string(256),true)]});
for (const [path, op, body, cap, issue, options] of [
 ['/profiles','createProfile','ProfileCommand','config.stage',43,{domain:'workspace',status:201,response:'Metadata'}],
 ['/profiles/{profile_id}','saveProfile','ProfileCommand','config.stage',43,{domain:'workspace',status:200,etag:true,method:'patch'}],
 ['/labels','createLabel','LabelCommand','workspace.write',43,{domain:'workspace',status:201,response:'Metadata'}],
 ['/labels/{label_id}','saveLabel','LabelCommand','workspace.write',43,{domain:'workspace',status:200,etag:true,method:'patch'}],
 ['/settings','changeSettings','SettingsCommand','workspace.write',54,{domain:'workspace',status:200,etag:true,method:'patch'}],
 ['/users','createUser','UserCreateCommand','user.manage',34,{sensitive:true}],
 ['/users/{user_id}','changeUser','UserUpdateCommand','user.manage',34,{etag:true,method:'patch'}],
 ['/users/{user_id}/password','changePassword','PasswordCommand','user.manage',34,{etag:true,sensitive:true}],
 ['/users/{user_id}/deletions','deleteUser','EmptyCommand','user.manage',34,{etag:true}],
 ['/roles','createRole','RoleCommand','role.manage',34,{}],
 ['/roles/{role_id}','changeRole','RoleCommand','role.manage',34,{etag:true,method:'patch'}],
 ['/tokens','createToken','TokenCommand','token.manage',34,{status:201,secretResponse:true}],
 ['/tokens/{token_id}/revocations','revokeToken','EmptyCommand','token.manage',34,{}],
 ['/aaa','configureAAA','AAACommand','access.configure',34,{etag:true,method:'patch'}],
 ['/certificates','createCertificate','CertificateCommand','certificate.manage',35,{sensitive:true}],
 ['/certificates/{certificate_id}/activations','activateCertificate','EmptyCommand','certificate.manage',35,{etag:true}],
 ['/certificates/{certificate_id}/confirmations','confirmCertificate','EmptyCommand','certificate.manage',35,{etag:true}],
 ['/diagnostics','createDiagnostic','DiagnosticCommand','diagnostic.run',46,{}],
 ['/captures','createCapture','CaptureCommand','capture.run',46,{}],
 ['/support-bundles','createSupportBundle','SupportCommand','support.export',46,{}],
 ['/jobs/{job_id}/cancellations','cancelJob','EmptyCommand','job.cancel',37,{}],
 ['/ovs-lifecycle/preflights','preflightOVSLifecycle','EmptyCommand','lifecycle.manage',49,{}],
 ['/ovs-lifecycle/commands','changeOVSLifecycle','LifecycleCommand','lifecycle.manage',49,{}],
 ['/checkpoints','createCheckpoint','EmptyCommand','recovery.manage',48,{}],
 ['/backups','createBackup','EmptyCommand','backup.manage',48,{}],
 ['/backups/{backup_id}/verifications','verifyBackup','EmptyCommand','backup.manage',48,{}],
 ['/restores','createRestore','RestoreCommand','recovery.manage',48,{}],
 ['/configuration/exports','exportConfiguration','EmptyCommand','config.export',48,{}],
 ['/configuration/imports','importConfiguration','ImportCommand','config.stage',48,{}],
 ['/artifacts','uploadArtifactPart','ArtifactCommand','artifact.write',48,{}],
 ['/openflow/preflights','preflightOpenFlow','OpenFlowPreflight','openflow.manage',53,{conditions:['expert','locally-managed','current-authority']}],
 ['/openflow/commands','executeOpenFlow','OpenFlowCommand','openflow.manage',53,{conditions:['expert','locally-managed','current-authority','validated-plan']}],
]) add(path, options.method ?? 'post', op, options.response ?? (options.secretResponse ? 'TokenCreated' : (options.status === 200 ? 'Metadata' : 'Accepted')), cap, issue, { domain: 'management', body, ...options });

// Artifact data is fetched separately from bounded JSON management responses.
add('/artifacts/{artifact_id}','get','readArtifact','Resource','artifact.read',48);
api['x-boundaries'] = { switching_changes: 'candidate-validation-transaction-evidence; public admission requires Safe Apply #40', websocket: 'notifications-only; REST resync is authoritative', dpdk_offload: 'observe', openflow_write: 'conditional service gate #53', authentication: 'mgrd per-operation authority #34', public_release: 'transport baseline does not claim product GA' };
s.Transaction.properties.field_execution_state = string(64);
s.Transaction.properties.candidate_id = id;
s.Transaction.properties.candidate_revision = id;
s.Transaction.properties.validation_id = id;
for (const path of ['/transactions', '/transactions/{transaction_id}', '/transactions/{transaction_id}/reconciliations', '/transactions/{transaction_id}/decisions']) {
 for (const op of Object.values(api.paths[path])) if (['listTransactions', 'readTransaction', 'reconcileTransaction', 'createTransaction', 'decideTransaction'].includes(op.operationId)) op['x-service-state'] = 'implemented';
}

export const pagePaths = {
 'OV-01':['/health','/ports','/workspace'], 'OV-02':['/search','/topology'], 'SW-01':['/bridges','/ports','/interfaces'], 'SW-02':['/bridges','/candidate'], 'SW-03':['/bridges/{bridge_id}','/ports'],
 'SW-04':['/ports','/candidate'], 'SW-05':['/ports/{port_id}','/candidate'], 'SW-06':['/interfaces'], 'SW-07':['/interfaces/{interface_id}'], 'SW-08':['/vlans','/candidate'],
 'SW-09':['/bonds','/candidate'], 'SW-10':['/spanning-tree','/candidate'], 'SW-11':['/multicast','/candidate'], 'SW-12':['/tunnels','/candidate'], 'SW-13':['/mirrors','/candidate'],
 'SW-14':['/qos','/candidate'], 'SW-15':['/isolation','/candidate'], 'SW-16':['/openflow/flows','/capabilities'], 'SW-17':['/openflow/flows','/openflow/preflights','/openflow/commands'],
 'SW-18':['/openflow/groups','/openflow/meters'], 'SW-19':['/openflow/controllers','/candidate'], 'VI-01':['/endpoints'], 'VI-02':['/fdb'], 'VI-03':['/neighbors'], 'VI-04':['/lldp'],
 'VI-05':['/statistics','/stream'], 'VI-06':['/dpdk'], 'VI-07':['/offload'], 'VI-08':['/telemetry','/candidate'], 'OP-01':['/health'], 'OP-02':['/events'], 'OP-03':['/diagnostics/templates','/diagnostics','/captures'],
 'OP-04':['/jobs'], 'OP-05':['/jobs/{job_id}','/jobs/{job_id}/cancellations'], 'OP-06':['/audit'], 'OP-07':['/support-bundles','/artifacts/{artifact_id}'],
 'CH-01':['/workspace','/candidate'], 'CH-02':['/validations','/validations/{validation_id}'], 'CH-03':['/transactions','/transactions/{transaction_id}','/transactions/{transaction_id}/decisions'],
 'CH-04':['/drift','/candidate'], 'CH-05':['/checkpoints'], 'AD-01':['/capabilities'], 'AD-02':['/management-network','/candidate'], 'AD-03':['/ovs-lifecycle','/ovs-lifecycle/commands'],
 'AD-04':['/backups','/restores'], 'AD-05':['/configuration/exports','/configuration/imports','/artifacts'], 'AD-06':['/users','/roles','/session'], 'AD-07':['/aaa'], 'AD-08':['/tokens'], 'AD-09':['/certificates'],
 'AD-10':['/settings'], 'AD-11':['/runtime','/health'], 'AD-12':['/about','/contract','/openapi.json'],
};
export const scopePaths = {
 'P1-PLAT-01':['/runtime'], 'P1-PLAT-02':['/capabilities','/validations'], 'P1-PLAT-03':['/candidate','/transactions'], 'P1-PLAT-04':['/capabilities','/interfaces'], 'P1-PLAT-05':['/workspace','/requests/{request_id}'],
 'P1-PLAT-06':['/certificates','/tokens'], 'P1-PLAT-07':['/candidate','/workspace'], 'P1-PLAT-08':['/transactions/{transaction_id}/decisions'], 'P1-PLAT-09':['/transactions/{transaction_id}'],
 'P1-PLAT-10':['/transactions/{transaction_id}/reconciliations'], 'P1-PLAT-11':['/ports','/requests/{request_id}'], 'P1-PLAT-12':['/stream','/jobs'],
 'P1-SW-01':['/bridges','/ports','/interfaces','/candidate'], 'P1-SW-02':['/vlans','/candidate'], 'P1-SW-03':['/bonds','/candidate'], 'P1-SW-04':['/spanning-tree','/candidate'], 'P1-SW-05':['/multicast','/candidate'],
 'P1-SW-06':['/profiles','/drift','/candidate'], 'P1-SW-07':['/mirrors','/candidate'], 'P1-SW-08':['/qos','/candidate'], 'P1-SW-09':['/tunnels','/candidate'], 'P1-SW-10':['/isolation','/candidate'],
 'P1-SW-11':['/openflow/flows','/openflow/groups','/openflow/meters','/openflow/commands'], 'P1-SW-12':['/dpdk'], 'P1-SW-13':['/offload'], 'P1-SW-14':['/telemetry','/candidate'], 'P1-SW-15':['/endpoints','/fdb','/neighbors','/lldp'], 'P1-SW-16':['/management-network','/candidate'],
 'P1-OPS-01':['/statistics','/health'], 'P1-OPS-02':['/events'], 'P1-OPS-03':['/health'], 'P1-OPS-04':['/diagnostics'], 'P1-OPS-05':['/captures'], 'P1-OPS-06':['/support-bundles'], 'P1-OPS-07':['/runtime','/health'], 'P1-OPS-08':['/stream','/ports'],
 'P1-SEC-01':['/sessions','/users'], 'P1-SEC-02':['/aaa'], 'P1-SEC-03':['/roles','/capabilities'], 'P1-SEC-04':['/sessions','/session'], 'P1-SEC-05':['/tokens'], 'P1-SEC-06':['/certificates'], 'P1-SEC-07':['/certificates','/tokens'], 'P1-SEC-08':['/audit'],
 'P1-API-01':['/contract'], 'P1-API-02':['/openapi.json'], 'P1-API-03':['/stream'], 'P1-API-04':['/contract'], 'P1-API-05':['/requests/{request_id}'], 'P1-API-06':['/jobs','/jobs/{job_id}'], 'P1-API-07':['/contract','/openapi.json'],
 'P1-LIFE-01':['/ovs-lifecycle/commands'], 'P1-LIFE-02':['/ovs-lifecycle/preflights'], 'P1-LIFE-03':['/checkpoints'], 'P1-LIFE-04':['/configuration/exports','/configuration/imports'], 'P1-LIFE-05':['/backups','/restores'], 'P1-LIFE-06':['/restores','/transactions/{transaction_id}/reconciliations'], 'P1-LIFE-07':['/runtime'],
};

// Optional target associations on durable evidence; unrelated resources remain unchanged.
s.Resource.properties.related_object_refs = array(ref('ResourceRef'), 32);
s.Job.properties.related_object_refs = array(ref('ResourceRef'), 32);

pagePaths['SW-10'].push('/inventory/spanning-tree', '/bridges/{bridge_id}/spanning-tree');
scopePaths['P1-SW-04'].push('/inventory/spanning-tree', '/bridges/{bridge_id}/spanning-tree');
