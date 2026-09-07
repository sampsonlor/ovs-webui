import type { Scenario } from './change-control';

export type Bridge = {
  name: string;
  state: 'Up' | 'Degraded' | 'Unknown';
  datapath: string;
  ports: number;
  interfaces: number;
  bonds: number;
  vlans: string;
  rstp: 'Enabled' | 'Disabled' | 'Unknown';
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Basic Manage' | 'Observe';
  uuid: string;
  provider: string;
};

export type Bond = {
  name: string;
  bridge: string;
  state: 'Up' | 'Degraded' | 'Unknown';
  mode: 'balance-tcp' | 'active-backup' | 'balance-slb';
  lacp: 'active' | 'passive' | 'off';
  members: string[];
  memberSpeeds: Array<number | null>;
  minLinks: number | null;
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Observe';
  uuid: string;
  provider: string;
};

// Bounded synthetic inventories; counts include children outside the detail sample.
export const bridges: Bridge[] = [
  {
    name: 'br-fabric',
    state: 'Up',
    datapath: 'system',
    ports: 8,
    interfaces: 10,
    bonds: 1,
    vlans: '10, 20, 120, 240',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: 'c44d…7b92',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-storage',
    state: 'Up',
    datapath: 'system',
    ports: 3,
    interfaces: 4,
    bonds: 1,
    vlans: '300–319',
    rstp: 'Disabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '2a1e…94f0',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-mgmt',
    state: 'Up',
    datapath: 'system',
    ports: 2,
    interfaces: 2,
    bonds: 0,
    vlans: '4094 native',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '8b9a…a110',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-offload',
    state: 'Unknown',
    datapath: 'netdev',
    ports: 5,
    interfaces: 5,
    bonds: 1,
    vlans: 'Provider-owned',
    rstp: 'Unknown',
    authority: 'External',
    scope: 'Observe',
    uuid: '6fce…004d',
    provider: 'SmartNIC provider',
  },
];

export const bonds: Bond[] = [
  {
    name: 'bond-uplink',
    bridge: 'br-fabric',
    state: 'Up',
    mode: 'balance-tcp',
    lacp: 'active',
    members: ['enp65s0f0', 'enp65s0f1'],
    memberSpeeds: [100, 100],
    minLinks: 1,
    authority: 'OVS',
    scope: 'Manage',
    uuid: '9f84…a3d1',
    provider: 'ovsdb-server',
  },
  {
    name: 'bond-storage',
    bridge: 'br-storage',
    state: 'Up',
    mode: 'active-backup',
    lacp: 'off',
    members: ['enp129s0f0', 'enp129s0f1'],
    memberSpeeds: [25, 25],
    minLinks: 1,
    authority: 'OVS',
    scope: 'Manage',
    uuid: '83cb…41c9',
    provider: 'ovsdb-server',
  },
  {
    name: 'bond-provider',
    bridge: 'br-offload',
    state: 'Unknown',
    mode: 'balance-slb',
    lacp: 'off',
    members: ['pf0hpf', 'pf1hpf'],
    memberSpeeds: [null, null],
    minLinks: null,
    authority: 'External',
    scope: 'Observe',
    uuid: 'b8d1…7e44',
    provider: 'SmartNIC provider',
  },
];

export function providerStale(scenario: Scenario) {
  return scenario === 'provider-degraded' || scenario === 'degraded';
}

export function bondObservation(bond: Bond, scenario: Scenario) {
  const unknown = bond.state === 'Unknown' || bond.authority === 'External';
  const memberDown = bond.name === 'bond-storage' && scenario === 'member-down';
  const mismatch = bond.name === 'bond-uplink' && scenario === 'lacp-mismatch';
  const state = unknown
    ? 'Unknown'
    : memberDown || mismatch
      ? 'Degraded'
      : bond.state;
  const negotiation = unknown
    ? 'Unknown'
    : mismatch
      ? 'Partner mismatch'
      : bond.lacp === 'off'
        ? 'Not enabled'
        : 'Negotiated';
  const freshness = unknown
    ? providerStale(scenario)
      ? 'Stale provider evidence'
      : 'Evidence unavailable'
    : 'Synthetic snapshot · 8s';
  const members = bond.members.map((name, index) => {
    const down = memberDown && index === 1;
    const standby = bond.mode === 'active-backup' && index > 0;
    const link = unknown ? 'Unknown' : down ? 'Down' : 'Up';
    return {
      name,
      link,
      speed:
        unknown || bond.memberSpeeds[index] == null
          ? 'Unknown'
          : down
            ? '—'
            : `${bond.memberSpeeds[index]} Gbps`,
      role: unknown
        ? 'Unknown'
        : down
          ? 'Inactive'
          : mismatch
            ? index === 0
              ? 'Collecting only'
              : 'Detached'
            : standby
              ? 'Standby'
              : 'Active',
      lacp: unknown
        ? 'Unknown'
        : bond.lacp === 'off'
          ? 'Not enabled'
          : down
            ? 'Detached'
            : mismatch
              ? index === 0
                ? 'Collecting only'
                : 'Detached'
              : 'Collecting + distributing',
      traffic:
        unknown || mismatch
          ? 'Unknown'
          : down || standby
            ? '0 bps'
            : bond.mode === 'active-backup'
              ? '18.4 Gbps'
              : index === 0
                ? '38.4 Gbps'
                : '34.9 Gbps',
    };
  });
  const capacity =
    unknown || bond.memberSpeeds.some((speed) => speed == null)
      ? 'Unknown'
      : mismatch
        ? 'Unverified'
        : bond.mode === 'active-backup'
          ? `${bond.memberSpeeds[0]} Gbps active`
          : `${bond.memberSpeeds.reduce<number>((total, speed) => total + (speed ?? 0), 0)} Gbps aggregate`;
  return {
    state,
    negotiation,
    freshness,
    members,
    capacity,
    unknown,
    memberDown,
    mismatch,
  };
}

export function bridgeObservation(bridge: Bridge, scenario: Scenario) {
  if (bridge.authority === 'External')
    return {
      state: 'Unknown',
      freshness: providerStale(scenario)
        ? 'Stale provider evidence'
        : 'Evidence unavailable',
    };
  const degraded = bonds.some(
    (bond) =>
      bond.bridge === bridge.name &&
      bondObservation(bond, scenario).state === 'Degraded',
  );
  return {
    state: degraded ? 'Degraded' : bridge.state,
    freshness: 'Synthetic snapshot · 8s',
  };
}

export const bridgeChildren: Record<
  string,
  Array<{
    name: string;
    kind: string;
    interfaces: string;
    state: string;
    vlan: string;
  }>
> = {
  'br-fabric': [
    {
      name: 'bond-uplink',
      kind: 'Bond Port',
      interfaces: 'enp65s0f0, enp65s0f1',
      state: 'Up',
      vlan: 'Trunk 10, 20, 120',
    },
    {
      name: 'server-07',
      kind: 'System Port',
      interfaces: 'enp129s0f1',
      state: 'Up',
      vlan: 'Access 120',
    },
    {
      name: 'server-08',
      kind: 'System Port',
      interfaces: 'enp129s0f2',
      state: 'Down',
      vlan: 'Access 120',
    },
  ],
  'br-storage': [
    {
      name: 'bond-storage',
      kind: 'Bond Port',
      interfaces: 'enp129s0f0, enp129s0f1',
      state: 'Up',
      vlan: 'Trunk 300–319',
    },
    {
      name: 'storage-node-01',
      kind: 'System Port',
      interfaces: 'enp129s0f2',
      state: 'Up',
      vlan: 'Access 310',
    },
  ],
  'br-mgmt': [
    {
      name: 'mgmt0',
      kind: 'System Port',
      interfaces: 'eno1',
      state: 'Up',
      vlan: 'Native 4094',
    },
    {
      name: 'mgmt-backup',
      kind: 'System Port',
      interfaces: 'eno2',
      state: 'Up',
      vlan: 'Native 4094',
    },
  ],
  'br-offload': [
    {
      name: 'bond-provider',
      kind: 'Bond Port',
      interfaces: 'pf0hpf, pf1hpf',
      state: 'Unknown',
      vlan: 'Provider-owned',
    },
    {
      name: 'rep0',
      kind: 'Provider Port',
      interfaces: 'pf2hpf',
      state: 'Unknown',
      vlan: 'Provider-owned',
    },
  ],
};

export function memberOptions(bridge: string, bond?: Bond) {
  const names =
    bridge === 'br-storage'
      ? ['enp129s0f0', 'enp129s0f1', 'enp129s0f2']
      : bridge === 'br-mgmt'
        ? ['eno1', 'eno2', 'enp4s0f0']
        : bridge === 'br-fabric'
          ? ['enp65s0f0', 'enp65s0f1', 'enp65s0f2', 'enp65s0f3']
          : [];
  return names.map((name) => {
    const owner = bridgeChildren[bridge]?.find((child) =>
      child.interfaces.split(', ').includes(name),
    )?.name;
    return { name, owner, available: !owner || owner === bond?.name };
  });
}

export type BondDraft = {
  name: string;
  bridge: string;
  mode: Bond['mode'];
  lacp: Bond['lacp'];
  minLinks: string;
  members: string[];
};

export function bondDraftErrors(draft: BondDraft, bond?: Bond) {
  const options = memberOptions(draft.bridge, bond);
  const portNames = [
    ...bonds.map((item) => item.name),
    ...Object.values(bridgeChildren)
      .flat()
      .map((item) => item.name),
  ];
  return {
    name: !/^[A-Za-z0-9_.-]{1,63}$/.test(draft.name)
      ? 'Use 1–63 letters, numbers, dots, underscores or hyphens.'
      : !bond && portNames.includes(draft.name)
        ? 'This Port name already exists in the synthetic inventory.'
        : null,
    bridge: !bridges.some(
      (item) => item.name === draft.bridge && item.scope === 'Manage',
    )
      ? 'Choose an OVS-managed Bridge.'
      : null,
    members:
      draft.members.length < 2
        ? 'Select at least two Interface members.'
        : new Set(draft.members).size !== draft.members.length ||
            draft.members.some(
              (name) =>
                !options.some(
                  (option) => option.name === name && option.available,
                ),
            )
          ? 'Members must belong to this Bond or be unassigned in the selected Bridge sample.'
          : null,
    policy:
      draft.mode === 'balance-tcp' && draft.lacp === 'off'
        ? 'balance-tcp requires LACP active or passive.'
        : null,
    minLinks:
      !draft.minLinks.trim() ||
      !Number.isInteger(Number(draft.minLinks)) ||
      Number(draft.minLinks) < 0 ||
      Number(draft.minLinks) > draft.members.length
        ? 'Minimum links must be an integer from 0 to the selected member count.'
        : null,
  };
}
