import type { Port, VlanValue } from './ovs-model';

export type ObjectKind = 'Bridge' | 'Port' | 'Interface';
export type ObjectReference = {
  instance: string;
  kind: ObjectKind;
  id: string;
  generation: number;
};
type Identity = { name: string; uuid: string };
export type BondPolicy = {
  mode: 'balance-tcp' | 'active-backup' | 'balance-slb';
  lacp: 'active' | 'passive' | 'off';
  minLinks: number | null;
  memberSpeeds: Array<number | null>;
  scope: 'Manage' | 'Observe';
};
export type InventoryPort = Identity & {
  bridge: string;
  interfaces: string[];
  state: Port['state'];
  speed: string;
  config: VlanValue;
  vlan: string;
  provider: string;
  authority: 'OVS' | 'External';
  scope: Port['scope'];
  core: boolean;
  bond?: BondPolicy;
};
export type InventoryInterface = Identity & {
  port: string;
  bridge: string;
  nativeType: 'system' | 'Unknown';
  source: string;
};
export type InventoryBridge = Identity & {
  state: 'Up' | 'Degraded' | 'Unknown';
  datapath: string;
  vlans: string;
  rstp: 'Enabled' | 'Disabled' | 'Unknown';
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Observe';
  provider: string;
};

// One bounded, synthetic relationship snapshot. No local/internal Port rows or
// hardware identity are inferred from names. Full host coverage is not claimed.
export const inventoryMetadata = {
  instance: 'ovs-synthetic-01',
  generation: 1842,
  source: 'synthetic-switching-inventory-v1',
  coverage: 'Bounded sample',
} as const;

export const inventoryBridges: InventoryBridge[] = [
  {
    name: 'br-fabric',
    uuid: 'c44d0000-0000-4000-8000-000000007b92',
    state: 'Up',
    datapath: 'system',
    vlans: '10, 20, 120, 240',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-storage',
    uuid: '2a1e0000-0000-4000-8000-0000000094f0',
    state: 'Up',
    datapath: 'system',
    vlans: '300–319',
    rstp: 'Disabled',
    authority: 'OVS',
    scope: 'Manage',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-mgmt',
    uuid: '8b9a0000-0000-4000-8000-00000000a110',
    state: 'Up',
    datapath: 'system',
    vlans: '4094 native',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-offload',
    uuid: '6fce0000-0000-4000-8000-00000000004d',
    state: 'Unknown',
    datapath: 'netdev',
    vlans: 'Provider-owned',
    rstp: 'Unknown',
    authority: 'External',
    scope: 'Observe',
    provider: 'SmartNIC provider',
  },
];

const trunk = (trunks: string): VlanValue => ({
  mode: 'trunk',
  tag: null,
  trunks,
});
const access = (tag: number): VlanValue => ({
  mode: 'access',
  tag,
  trunks: '',
});
const management: VlanValue = {
  mode: 'native-tagged',
  tag: 4094,
  trunks: '4094',
};
const managed = {
  state: 'Up',
  authority: 'OVS',
  provider: 'system',
  scope: 'Basic Manage',
} as const;
const external = {
  state: 'Unknown',
  authority: 'External',
  provider: 'SmartNIC',
  scope: 'Observe',
  speed: 'Unknown',
  vlan: 'Provider-owned',
  config: { mode: 'access', tag: null, trunks: '' },
} as const;

// The first six preserve the P0/lab object selection and lab resource ordering.
// P1 fixture corrections are recorded in SHARED_INVENTORY_v0.1.md.
export const inventoryPorts: InventoryPort[] = [
  {
    ...managed,
    name: 'uplink-01',
    uuid: '84a90000-0000-4000-8000-000000001fc2',
    bridge: 'br-fabric',
    interfaces: ['enp65s0f0'],
    speed: '100 Gbps',
    vlan: 'Trunk · 10, 20, 120',
    config: trunk('10, 20, 120'),
    scope: 'Manage',
    core: true,
  },
  {
    ...managed,
    name: 'server-07',
    uuid: '29fd0000-0000-4000-8000-000000008a71',
    bridge: 'br-fabric',
    interfaces: ['enp129s0f1'],
    speed: '25 Gbps',
    vlan: 'Access · 120',
    config: access(120),
    scope: 'Manage',
    core: true,
  },
  {
    ...managed,
    name: 'server-08',
    uuid: '742e0000-0000-4000-8000-0000000053b1',
    bridge: 'br-fabric',
    interfaces: ['enp129s0f2'],
    state: 'Down',
    speed: '—',
    vlan: 'Access · 120',
    config: access(120),
    core: true,
  },
  {
    ...managed,
    name: 'bond-storage',
    uuid: '83cb0000-0000-4000-8000-0000000041c9',
    bridge: 'br-storage',
    interfaces: ['enp130s0f0', 'enp130s0f1'],
    speed: '25 Gbps active',
    vlan: 'Trunk · 300–319',
    config: trunk('300-319'),
    core: true,
    bond: {
      mode: 'active-backup',
      lacp: 'off',
      minLinks: 1,
      memberSpeeds: [25, 25],
      scope: 'Manage',
    },
  },
  {
    ...external,
    name: 'rep0',
    uuid: '2e130000-0000-4000-8000-00000000c0d7',
    bridge: 'br-offload',
    interfaces: ['pf0hpf'],
    core: true,
  },
  {
    ...managed,
    name: 'mgmt0',
    uuid: 'b12c0000-0000-4000-8000-00000000832a',
    bridge: 'br-mgmt',
    interfaces: ['eno1'],
    speed: '1 Gbps',
    vlan: 'Native · 4094',
    config: management,
    core: true,
  },
  {
    ...managed,
    name: 'bond-uplink',
    uuid: '9f840000-0000-4000-8000-00000000a3d1',
    bridge: 'br-fabric',
    interfaces: ['enp66s0f0', 'enp66s0f1'],
    speed: '200 Gbps aggregate',
    vlan: 'Trunk · 10, 20, 120',
    config: trunk('10, 20, 120'),
    scope: 'Manage',
    core: false,
    bond: {
      mode: 'balance-tcp',
      lacp: 'active',
      minLinks: 1,
      memberSpeeds: [100, 100],
      scope: 'Manage',
    },
  },
  {
    ...managed,
    name: 'storage-node-01',
    uuid: 'e0010000-0000-4000-8000-000000000001',
    bridge: 'br-storage',
    interfaces: ['enp130s0f2'],
    speed: '25 Gbps',
    vlan: 'Access · 310',
    config: access(310),
    core: false,
  },
  {
    ...managed,
    name: 'mgmt-backup',
    uuid: 'e0020000-0000-4000-8000-000000000002',
    bridge: 'br-mgmt',
    interfaces: ['eno2'],
    speed: '1 Gbps',
    vlan: 'Native · 4094',
    config: management,
    core: false,
  },
  {
    ...external,
    name: 'bond-provider',
    uuid: 'b8d10000-0000-4000-8000-000000007e44',
    bridge: 'br-offload',
    interfaces: ['pf1hpf', 'pf2hpf'],
    core: false,
    bond: {
      mode: 'balance-slb',
      lacp: 'off',
      minLinks: null,
      memberSpeeds: [null, null],
      scope: 'Observe',
    },
  },
];

// Explicit stable Interface UUIDs; never derive hardware type from a name.
const interfaceFacts: Record<
  string,
  [string, InventoryInterface['nativeType']]
> = {
  enp65s0f0: ['f0010000-0000-4000-8000-000000000001', 'system'],
  enp129s0f1: ['f0020000-0000-4000-8000-000000000002', 'system'],
  enp129s0f2: ['f0030000-0000-4000-8000-000000000003', 'system'],
  enp130s0f0: ['f0040000-0000-4000-8000-000000000004', 'system'],
  enp130s0f1: ['f0050000-0000-4000-8000-000000000005', 'system'],
  pf0hpf: ['f0060000-0000-4000-8000-000000000006', 'Unknown'],
  eno1: ['f0070000-0000-4000-8000-000000000007', 'system'],
  enp66s0f0: ['f0080000-0000-4000-8000-000000000008', 'system'],
  enp66s0f1: ['f0090000-0000-4000-8000-000000000009', 'system'],
  enp130s0f2: ['f00a0000-0000-4000-8000-00000000000a', 'system'],
  eno2: ['f00b0000-0000-4000-8000-00000000000b', 'system'],
  pf1hpf: ['f00c0000-0000-4000-8000-00000000000c', 'Unknown'],
  pf2hpf: ['f00d0000-0000-4000-8000-00000000000d', 'Unknown'],
};
export const inventoryInterfaces: InventoryInterface[] = inventoryPorts.flatMap(
  (port) =>
    port.interfaces.map((name) => ({
      name,
      uuid: interfaceFacts[name][0],
      port: port.name,
      bridge: port.bridge,
      nativeType: interfaceFacts[name][1],
      source: inventoryMetadata.source,
    })),
);

// Host attachment candidates are not unreferenced OVS Interface rows. They are
// excluded from the native inventory counts and cannot own a Port in this snapshot.
export const attachmentCandidates = [
  { name: 'enp65s0f2', bridge: 'br-fabric' },
  { name: 'enp65s0f3', bridge: 'br-fabric' },
  { name: 'enp4s0f0', bridge: 'br-mgmt' },
];

export const portPresentations: Port[] = inventoryPorts.map((port) => ({
  ...port,
  interfaceName: port.interfaces.join(', '),
  ...(port.bond ? { members: [...port.interfaces] } : {}),
}));

export function objectReference(
  kind: ObjectKind,
  object: Identity,
): ObjectReference {
  return {
    instance: inventoryMetadata.instance,
    generation: inventoryMetadata.generation,
    kind,
    id: object.uuid,
  };
}
export type ResolvedObject =
  | {
      status: 'found';
      kind: 'Bridge';
      object: InventoryBridge;
      reference: ObjectReference;
    }
  | {
      status: 'found';
      kind: 'Port';
      object: InventoryPort;
      reference: ObjectReference;
    }
  | {
      status: 'found';
      kind: 'Interface';
      object: InventoryInterface;
      reference: ObjectReference;
    }
  | { status: 'unavailable'; target: string; reason: string };

export function resolveInventoryObject(
  target: string | ObjectReference,
): ResolvedObject {
  const label =
    typeof target === 'string' ? target : `${target.kind}/${target.id}`;
  const missing = (reason: string): ResolvedObject => ({
    status: 'unavailable',
    target: label,
    reason,
  });
  let kind: string, id: string;
  if (typeof target === 'string') {
    const parts = target.split('/');
    if (parts.length !== 2)
      return missing('Use one explicit Bridge, Port or Interface reference.');
    [kind, id] = parts;
  } else {
    if (target.instance !== inventoryMetadata.instance)
      return missing('This reference belongs to another instance.');
    if (target.generation !== inventoryMetadata.generation)
      return missing('This reference belongs to another inventory generation.');
    ({ kind, id } = target);
  }
  // UUID references are resolved strictly by UUID. Legacy Kind/name targets are
  // admitted only within this named snapshot; no default object is substituted.
  const matches = (object: Identity) =>
    typeof target === 'string'
      ? object.name === id || object.uuid === id
      : object.uuid === id;
  if (kind === 'Bridge') {
    const object = inventoryBridges.find(matches);
    if (object)
      return {
        status: 'found',
        kind,
        object,
        reference: objectReference(kind, object),
      };
  } else if (kind === 'Port') {
    const object = inventoryPorts.find(matches);
    if (object)
      return {
        status: 'found',
        kind,
        object,
        reference: objectReference(kind, object),
      };
  } else if (kind === 'Interface') {
    const object = inventoryInterfaces.find(matches);
    if (object)
      return {
        status: 'found',
        kind,
        object,
        reference: objectReference(kind, object),
      };
  } else
    return missing('This object kind is not part of the switching inventory.');
  return missing(
    'This target is outside the current bounded inventory. A confirmed intent is not an inventory observation.',
  );
}

export type ObjectFacet = 'port' | 'bond' | 'vlan' | 'bond-edit';
export function objectHash(reference: ObjectReference, facet?: ObjectFacet) {
  const path = [reference.instance, reference.kind, reference.id]
    .map(encodeURIComponent)
    .join('/');
  return `#object/${path}?generation=${reference.generation}${facet ? `&facet=${facet}` : ''}`;
}
export function parseObjectHash(
  hash: string,
): { target: ObjectReference; facet?: ObjectFacet } | null {
  const match = /^#object\/([^/]+)\/([^/]+)\/([^?]+)\?(.+)$/.exec(hash);
  if (!match) return null;
  try {
    const [, instance, kind, id, query] = match;
    const params = new URLSearchParams(query);
    const generation = Number(params.get('generation'));
    const facet = params.get('facet');
    if (
      !params.has('generation') ||
      !Number.isSafeInteger(generation) ||
      generation < 1 ||
      !['Bridge', 'Port', 'Interface'].includes(decodeURIComponent(kind)) ||
      (facet !== null && !['port', 'bond', 'vlan', 'bond-edit'].includes(facet))
    )
      return null;
    return {
      target: {
        instance: decodeURIComponent(instance),
        kind: decodeURIComponent(kind) as ObjectKind,
        id: decodeURIComponent(id),
        generation,
      },
      ...(facet ? { facet: facet as ObjectFacet } : {}),
    };
  } catch {
    return null;
  }
}
