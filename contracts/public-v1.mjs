// Public transport source. Service availability is distinct from wire coverage.
import { readFileSync } from 'node:fs';
const proposal = JSON.parse(readFileSync(new URL('./proposals/phase1-v1.openapi.json', import.meta.url)));
export const api = structuredClone(proposal);
api.info = { title: 'OVS WebUI public API', version: '1.23.0', description: 'Phase 1 typed VLAN, Bond/LACP and managed isolated Bridge creation/deletion through Candidate and durable Safe Apply. Deletion compensation uses fresh identities and shared evidence. Independent internal access Port creation and deletion on an explicitly authorized existing Bridge are also supported; deletion recovery preserves the parent and uses fresh child identities. QinQ and customer VLAN lists are supported on eligible existing single-interface system Ports with the native TPID preserved. Interface inventory exposes native field provenance, requested versus observed MTU and reported device status through list/detail pages. Explicit and proven automatic MTU transitions on authorized standalone non-local internal Interfaces use Candidate and Safe Apply with actual device MTU proof and sealed default dependencies. Interface detail adds bounded, independently sourced Linux device observations and exact sysfs hardware associations after matching the native name and ifindex. Interface configuration observes requested port allocation and four ingress policing parameters with explicit native units, unsupported schemas and configuration permissions; configuration does not prove runtime allocation or enforcement. Interface patch configuration resolves reciprocal peers within a fresh snapshot without inferring operational connectivity. Interface MTU lifecycle evidence carries bounded, server-derived immutable target references for shared object-scoped Events/Audit navigation; retained history is not complete history. Interface detail independently observes bounded Linux ingress tc police actions, with explicit kernel units, partial coverage and identity/configuration permission gates. Installed rules are not measured traffic effectiveness or installer ownership. General graph deletion, physical/member/other Interface mutations and full pages retain separate gates.' };
api.info.description += ' Controlled ingress policing supports one bandwidth or packet rate, or disable, on independently authorized standalone internal Interfaces with native default bursts preserved. A root policy declares exclusive software ingress management. Dispatch, Applied and compensation require qualified kernel and guarded native configuration evidence; neither proves measured traffic effectiveness.';
api['x-review-status'] = 'implementation-review';
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
Object.assign(s.Interface.properties, { bridge_ref: ref('ResourceRef'), port_kind: string(), internal: nullable(bool), local_interface: nullable(bool), ownership: string(), allowed_operations: array(string(), 64), fields: { type: 'object', additionalProperties: ref('InventoryField') } });
s.Interface.description = 'Native Interface inventory with explicitly admitted MTU edits. Configuration fields require configuration.read; operational fields remain observations. Empty optional values, unavailable columns and withheld values remain distinct. No hardware role or Interface type is inferred from names. MTU editing requires independent root Interface authority on a standalone non-local internal Interface. Empty native requests require proven default dependencies.';
Object.assign(s.Interface.properties, { mtu_ownership: string(), mtu_editable: bool, mtu_clearable: bool, mtu_default: integer(576,65535), mtu_default_reason: {type:'string', maxLength:256} });
Object.assign(s.Interface.properties, { policing_ownership: string(), policing_editable: bool });
s.LinuxDeviceField = open({value: {}, availability: string(), reason: {type:'string', maxLength:128}});
s.LinuxDeviceObservation = open({availability: string(), reason: {type:'string', maxLength:128}, ifindex: nullable(integer(1,2147483647)), source: ref('Source'), fields: {type:'object', maxProperties:10, additionalProperties:ref('LinuxDeviceField')}});
s.LinuxDeviceObservation.description = 'Detail-only read observation. Native OVS name/type/ifindex and confirmed, fresh identity gate a bounded Linux sysfs sample; association is rechecked after reading. Carrier, operstate, MTU and speed remain distinct from OVS values. PCI requires the first actual ancestor subsystem to be PCI; OVS bus_info is only a reported hint. Unavailable fields are not replaced with defaults. No paths, host attachment candidates, write authority, caching or device mutation.';
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
Object.assign(s, intentSchemas);
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
s.Transaction.properties.identity_replacements = array(ref('IdentityReplacement'), 3);
s.ObservedIntent.properties.operation['x-known-values'] = ['port.vlan.set', 'bond.configure', 'port.lacp.set', 'bridge.create-isolated', 'bridge.delete-isolated', 'port.create-internal', 'port.delete-internal'];
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
