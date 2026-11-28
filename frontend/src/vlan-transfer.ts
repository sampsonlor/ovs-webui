import type { Port, PortsPage, Session, VLANConfigurationImportCommand } from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';

export const vlanTransferRoute = '/admin/configuration-transfer';
export const vlanExportPath = '/configuration/vlan-export';
export const vlanImportPath = '/configuration/vlan-imports';
export const vlanTransferMaxPorts = 8;
export const vlanTransferMaxBytes = 24 * 1024;

const format = 'ovs-webui.port-vlan.v1';
const managementID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const nativeID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const shaPattern = /^[a-f0-9]{64}$/;
const modes = ['access', 'trunk', 'native-tagged', 'native-untagged'] as const;

type Binding = { management_id: string; ovs_uuid: string; table: 'Port' | 'Interface'; instance_generation: string };
export type VlanTransferFields = { vlan_mode?: typeof modes[number]; tag?: number[]; trunks?: number[] };
export type VlanTransferPort = { binding: Binding; member: Binding; shape: 'standalone-system' | 'standalone-internal'; vlan: VlanTransferFields };
export type VlanTransferDocument = { format: typeof format; instance_generation: string; schema_digest: string; ports: VlanTransferPort[] };
export type LoadedVlanTransfer = { document: VlanTransferDocument; encoded: string; bytes: number };
export type VlanTransferMapping = VLANConfigurationImportCommand['mappings'][number];
export type VlanTransferMode = NonNullable<VLANConfigurationImportCommand['mode']>;
export type TransferTarget = { id: string; name: string; member: string; generation: string };

export const canExportVlans = (session: Session | null) =>
  ['state.read', 'inventory.read', 'configuration.read'].every((capability) => has(session, capability));
export const canStageVlans = (session: Session | null) =>
  ['configuration.import', 'workspace.write', 'ovs.port.vlan.write'].every((capability) => has(session, capability));
export const canUseVlanTransfer = (session: Session | null) => canExportVlans(session) || canStageVlans(session);

export function vlanTransferQuery(query: string) {
  const params = new URLSearchParams(query);
  return { filter: (params.get('filter') ?? '').slice(0, 128), cursor: params.get('cursor') ?? '' };
}

export function vlanTransferURL(filter: string, cursor = '', inventory = false): string {
  const query = new URLSearchParams(inventory ? { limit: '25' } : {});
  if (filter) query.set('filter', filter);
  if (cursor) query.set('cursor', cursor);
  const path = inventory ? '/ports' : vlanTransferRoute;
  return path + (query.size ? `?${query}` : '');
}

export function currentVlanTransferInventory(page: PortsPage | null): PortsPage | null {
  return page?.source?.freshness === 'fresh' && page.availability === 'complete' && managementID.test(page.instance_generation) &&
    Array.isArray(page.items) ? page : null;
}

export function vlanTransferTarget(port: Port, generation: string): TransferTarget | null {
  const member = port.interface_refs?.[0];
  return port.kind === 'single' && port.local_port === false && port.interface_refs?.length === 1 && member?.kind === 'interface' &&
    managementID.test(port.management_id) && managementID.test(member.id) && port.instance_generation === generation
    ? { id: port.management_id, name: port.name, member: member.id, generation }
    : null;
}

export function vlanExportURL(ids: readonly string[]): string | null {
  if (!ids.length || ids.length > vlanTransferMaxPorts || new Set(ids).size !== ids.length || ids.some((id) => !managementID.test(id))) return null;
  return `${vlanExportPath}?${new URLSearchParams({ ports: ids.join(',') })}`;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function fields(value: object, allowed: string[]) {
  return Object.keys(value).every((key) => allowed.includes(key));
}

function binding(value: unknown, table: string, generation: string): value is Binding {
  return record(value) && fields(value, ['management_id', 'ovs_uuid', 'table', 'instance_generation']) &&
    typeof value.management_id === 'string' && managementID.test(value.management_id) &&
    typeof value.ovs_uuid === 'string' && nativeID.test(value.ovs_uuid) && value.table === table && value.instance_generation === generation;
}

function vlanSet(value: unknown, max: number): value is number[] {
  return Array.isArray(value) && value.length <= max && value.every((vlan) => Number.isInteger(vlan) && vlan >= 1 && vlan <= 4094) &&
    new Set(value).size === value.length;
}

function documentShape(value: unknown): value is VlanTransferDocument {
  if (!record(value) || !fields(value, ['format', 'instance_generation', 'schema_digest', 'ports']) || value.format !== format ||
      typeof value.instance_generation !== 'string' || !managementID.test(value.instance_generation) ||
      typeof value.schema_digest !== 'string' || !shaPattern.test(value.schema_digest) ||
      !Array.isArray(value.ports) || !value.ports.length || value.ports.length > vlanTransferMaxPorts) return false;
  const generation = value.instance_generation;
  const idSet = new Set<string>(), nativeSet = new Set<string>();
  for (const port of value.ports) {
    if (!record(port) || !fields(port, ['binding', 'member', 'shape', 'vlan']) || !binding(port.binding, 'Port', generation) ||
        !binding(port.member, 'Interface', generation) || (port.shape !== 'standalone-system' && port.shape !== 'standalone-internal')) return false;
    const vlan = port.vlan;
    if (!record(vlan) || !fields(vlan, ['vlan_mode', 'tag', 'trunks']) ||
        ('vlan_mode' in vlan && !modes.some((mode) => mode === vlan.vlan_mode)) ||
        ('tag' in vlan && !vlanSet(vlan.tag, 1)) || ('trunks' in vlan && !vlanSet(vlan.trunks, 4094))) return false;
    for (const item of [port.binding, port.member]) {
      if (idSet.has(item.management_id) || nativeSet.has(item.ovs_uuid)) return false;
      idSet.add(item.management_id);
      nativeSet.add(item.ovs_uuid);
    }
  }
  return true;
}

export function readVlanTransferFile(bytes: Uint8Array<ArrayBuffer>): LoadedVlanTransfer {
  if (!bytes.length || bytes.length > vlanTransferMaxBytes) throw new Error('CONFIG_DOCUMENT_LIMIT');
  let document: unknown;
  try {
    document = JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes));
  } catch {
    throw new Error('CONFIG_DOCUMENT_INVALID');
  }
  if (!documentShape(document)) throw new Error('CONFIG_DOCUMENT_INVALID');
  return { document, encoded: btoa(String.fromCharCode(...bytes)), bytes: bytes.length };
}

export async function verifyVlanExport(encoded: string, checksum: string, ids: readonly string[], generation: string) {
  if (typeof encoded !== 'string' || encoded.length > 4 * Math.ceil(vlanTransferMaxBytes / 3) ||
      !/^[A-Za-z0-9+/]+={0,2}$/.test(encoded) || !shaPattern.test(checksum)) throw new Error('CONFIG_DOCUMENT_INVALID');
  const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
  const file = readVlanTransferFile(bytes);
  if (file.document.instance_generation !== generation || file.document.ports.length !== ids.length ||
      !file.document.ports.every(({ binding }) => ids.includes(binding.management_id))) throw new Error('CONFIG_EXPORT_SCOPE_CHANGED');
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
  const hex = Array.from(digest, (value) => value.toString(16).padStart(2, '0')).join('');
  if (hex !== checksum) throw new Error('CONFIG_EXPORT_CHECKSUM_MISMATCH');
  return bytes;
}

export function hasExactVlanFields(document: VlanTransferDocument): boolean {
  return document.ports.every(({ vlan }) =>
    vlan.vlan_mode !== undefined && vlan.tag !== undefined && vlan.trunks !== undefined &&
    (vlan.vlan_mode === 'trunk' ? vlan.tag.length === 0 : vlan.tag.length === 1) &&
    (vlan.vlan_mode !== 'access' || vlan.trunks.length === 0));
}

export function vlanTransferMappings(document: VlanTransferDocument, generation: string, manual: boolean,
  destinations: Record<string, TransferTarget>, mode: VlanTransferMode = 'merge'): VlanTransferMapping[] | null {
  if (!managementID.test(generation)) return null;
  if (!manual && mode === 'merge') return document.instance_generation === generation ? [] : null;
  if (mode === 'exact-restore' && !hasExactVlanFields(document)) return null;
  const mappings: VlanTransferMapping[] = [];
  const used = new Set<string>();
  for (const { binding } of document.ports) {
    const destination = destinations[binding.management_id];
    if (!destination || destination.generation !== generation || !managementID.test(destination.id) || used.has(destination.id)) return null;
    used.add(destination.id);
    const mapping: VlanTransferMapping = { source_id: binding.management_id, destination_id: destination.id };
    if (mode === 'exact-restore') {
      if (!managementID.test(destination.member) || used.has(destination.member)) return null;
      used.add(destination.member);
      mapping.destination_member_id = destination.member;
    }
    mappings.push(mapping);
  }
  return mappings;
}

export function vlanTransferError(code: string): string {
  if (code === 'CONFIG_DOCUMENT_LIMIT') return 'Choose a nonempty Port VLAN JSON file up to 24 KiB.';
  if (code === 'CONFIG_EXPORT_CHECKSUM_MISMATCH') return 'The exported bytes did not match the checksum.';
  if (code === 'CONFIG_EXPORT_SCOPE_CHANGED') return 'The export did not match the selected Ports and generation.';
  if (code === 'CONFIG_DOCUMENT_INVALID') return 'The file is invalid for the managed Port VLAN format.';
  if (code === 'CONFIG_MAPPING_REQUIRED') return 'Review explicit destination mappings before staging again.';
  if (code === 'CONFIG_EXACT_FIELDS_REQUIRED') return 'Exact Restore requires an explicit VLAN mode, tag and trunk list for every source Port.';
  if (code === 'CONFIG_DESTINATION_MEMBER_CHANGED') return 'A destination member changed. Refresh the Port inventory and review the mappings again.';
  if (code === 'CONFIG_EXACT_BEFORE_UNSUPPORTED') return 'A destination has VLAN settings outside the supported Exact Restore scope.';
  if (code === 'CONFIG_PORT_UNPROVEN' || code === 'CONFIG_PORT_SHAPE_UNPROVEN') return 'The current Port and its member do not qualify for this VLAN transfer.';
  return 'The configuration transfer could not be completed.';
}
