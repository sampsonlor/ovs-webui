import { inventoryPorts, portPresentations } from './inventory-model.ts';
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
  | 'interface-detail'
  | 'object-unavailable'
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

export const ports: Port[] = portPresentations;
// The persisted lab remains the original six-Port contract slice.
export const corePorts: Port[] = ports.filter((port) =>
  inventoryPorts.some((item) => item.uuid === port.uuid && item.core),
);

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
