import type { P1View } from '../app/prototype-model';

export type Mode = 'standard' | 'expert';
export type View =
  | 'dashboard'
  | 'ports'
  | 'port-detail'
  | 'vlan-edit'
  | 'workspace'
  | 'diff'
  | 'safe-apply'
  | 'evidence'
  | 'responsive'
  | P1View;
export type VlanMode = 'access' | 'trunk' | 'native-tagged';
export type VlanValue = { mode: VlanMode; tag: number | null; trunks: string };
export type Port = {
  name: string;
  state: 'Up' | 'Down' | 'Unknown';
  speed: string;
  vlan: string;
  config: VlanValue;
  bridge: string;
  interfaceName: string;
  members?: string[];
  provider: string;
  authority: 'OVS' | 'External' | 'Unknown';
  scope: 'Manage' | 'Basic Manage' | 'Observe';
  uuid: string;
};

// Synthetic fixtures from the frozen P0 prototype. No device is connected.
export const ports: Port[] = [
  {
    name: 'uplink-01',
    state: 'Up',
    speed: '100 Gbps',
    vlan: 'Trunk · 10, 20, 120',
    config: { mode: 'trunk', tag: null, trunks: '10, 20, 120' },
    bridge: 'br-fabric',
    interfaceName: 'enp65s0f0',
    provider: 'system',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '84a9…1fc2',
  },
  {
    name: 'server-07',
    state: 'Up',
    speed: '25 Gbps',
    vlan: 'Access · 120',
    config: { mode: 'access', tag: 120, trunks: '' },
    bridge: 'br-fabric',
    interfaceName: 'enp129s0f1',
    provider: 'system',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '29fd…8a71',
  },
  {
    name: 'server-08',
    state: 'Down',
    speed: '—',
    vlan: 'Access · 120',
    config: { mode: 'access', tag: 120, trunks: '' },
    bridge: 'br-fabric',
    interfaceName: 'enp129s0f2',
    provider: 'system',
    authority: 'OVS',
    scope: 'Basic Manage',
    uuid: '742e…53b1',
  },
  {
    name: 'bond-storage',
    state: 'Up',
    speed: '50 Gbps',
    vlan: 'Trunk · 300–319',
    config: { mode: 'trunk', tag: null, trunks: '300-319' },
    bridge: 'br-storage',
    interfaceName: 'enp130s0f0, enp130s0f1',
    members: ['enp130s0f0', 'enp130s0f1'],
    provider: 'system',
    authority: 'OVS',
    scope: 'Basic Manage',
    uuid: '83cb…41c9',
  },
  {
    name: 'rep0',
    state: 'Unknown',
    speed: 'Unknown',
    vlan: 'Provider-owned',
    config: { mode: 'access', tag: null, trunks: '' },
    bridge: 'br-offload',
    interfaceName: 'pf0hpf',
    provider: 'SmartNIC',
    authority: 'External',
    scope: 'Observe',
    uuid: '2e13…c0d7',
  },
  {
    name: 'mgmt0',
    state: 'Up',
    speed: '1 Gbps',
    vlan: 'Native · 4094',
    config: { mode: 'native-tagged', tag: 4094, trunks: '4094' },
    bridge: 'br-mgmt',
    interfaceName: 'eno1',
    provider: 'system',
    authority: 'OVS',
    scope: 'Basic Manage',
    uuid: 'b12c…832a',
  },
];

export function vlanLabel(value: VlanValue): string {
  if (value.mode === 'access') return `Access · VLAN ${value.tag ?? 'unset'}`;
  if (value.mode === 'native-tagged')
    return `Native tagged · ${value.tag ?? 'unset'} · allowed ${value.trunks}`;
  return `Trunk · ${value.trunks}`;
}

/** Normalized OVS fields for the Expert preview, not a wire transaction. */
export function nativeVlanFields(value: VlanValue) {
  const trunks = new Set<number>();
  for (const part of value.trunks.split(',').filter(Boolean)) {
    const [start, end = start] = part.trim().split('-').map(Number);
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 1 ||
      end > 4094 ||
      start > end
    )
      continue;
    for (let id = start; id <= end; id++) trunks.add(id);
  }
  return {
    vlan_mode: value.mode,
    tag: value.tag,
    trunks: [...trunks].sort((a, b) => a - b),
  };
}
