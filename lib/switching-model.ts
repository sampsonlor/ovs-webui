import {
  inventoryBridges,
  inventoryPorts,
  inventoryInterfaces,
  attachmentCandidates,
} from './inventory-model.ts';
import type { Scenario } from './change-control';
import type { ChangeIntent } from '../app/prototype-model';
import { ports, vlanLabel, type VlanValue } from './ovs-model.ts';

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

// Every page projects the same bounded relationship snapshot.
export const bridges: Bridge[] = inventoryBridges.map((bridge) => {
  const children = inventoryPorts.filter((port) => port.bridge === bridge.name);
  return {
    ...bridge,
    ports: children.length,
    interfaces: children.reduce(
      (count, port) => count + port.interfaces.length,
      0,
    ),
    bonds: children.filter((port) => port.bond).length,
  };
});
export const bonds: Bond[] = inventoryPorts
  .filter((port) => port.bond)
  .map((port) => ({
    ...port,
    ...port.bond!,
    state: port.state === 'Down' ? 'Degraded' : port.state,
    members: [...port.interfaces],
    provider: port.authority === 'OVS' ? 'ovsdb-server' : 'SmartNIC provider',
  }));
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

export const bridgeChildren = Object.fromEntries(
  inventoryBridges.map((bridge) => [
    bridge.name,
    inventoryPorts
      .filter((port) => port.bridge === bridge.name)
      .map((port) => ({
        name: port.name,
        kind: port.bond
          ? 'Bond Port'
          : port.authority === 'External'
            ? 'Provider Port'
            : 'System Port',
        interfaces: port.interfaces.join(', '),
        state: port.state,
        vlan: port.vlan,
      })),
  ]),
);

export function currentBridgeChildren(
  bridge: string,
  live: Record<string, VlanValue> = {},
) {
  return (bridgeChildren[bridge] ?? []).map((child) => ({
    ...child,
    vlan: live[child.name] ? vlanLabel(live[child.name]) : child.vlan,
  }));
}

export function memberOptions(bridge: string, bond?: Bond) {
  return [
    ...inventoryInterfaces
      .filter((item) => item.bridge === bridge)
      .map((item) => ({
        name: item.name,
        owner: item.port,
        available: item.port === bond?.name,
      })),
    ...attachmentCandidates
      .filter(
        (item) =>
          item.bridge === bridge &&
          !inventoryInterfaces.some((native) => native.name === item.name),
      )
      .map((item) => ({ name: item.name, owner: undefined, available: true })),
  ];
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
    ...ports.map((item) => item.name),
    ...bonds.map((item) => item.name),
    ...Object.values(bridgeChildren)
      .flat()
      .map((item) => item.name),
  ];
  return {
    name: !/^[A-Za-z0-9_.-]{1,63}$/.test(draft.name)
      ? 'Use 1–63 letters, numbers, dots, underscores or hyphens.'
      : bond && draft.name !== bond.name
        ? 'An existing Bond Port name cannot be changed.'
        : !bond && portNames.includes(draft.name)
          ? 'This Port name already exists in the synthetic inventory.'
          : null,
    bridge:
      bond && draft.bridge !== bond.bridge
        ? 'An existing Bond cannot move to another Bridge in this editor.'
        : !bridges.some(
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
      !['balance-tcp', 'active-backup', 'balance-slb'].includes(draft.mode) ||
      !['active', 'passive', 'off'].includes(draft.lacp)
        ? 'Choose a supported Bond mode and LACP policy.'
        : draft.mode === 'balance-tcp' && draft.lacp === 'off'
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

export function bondNativeValue(draft: BondDraft, advanced = false) {
  return [
    `bridge: ${draft.bridge}`,
    `bond_mode: ${draft.mode}`,
    `lacp: ${draft.lacp}`,
    `other_config:min-links: ${draft.minLinks}`,
    `interfaces: [${[...draft.members].sort().join(', ')}]`,
    ...(advanced ? ['other_config:bond-rebalance-interval: 10000'] : []),
  ].join('\n');
}

// UI and programmatic creation share both validation and native mapping.
export function bondChangeIntent(
  draft: BondDraft,
  bond?: Bond,
  advanced = false,
  memberDown = false,
): ChangeIntent {
  if (bond?.scope === 'Observe') throw new Error('This Bond is Observe-only.');
  const error = Object.values(bondDraftErrors(draft, bond)).find(Boolean);
  if (error) throw new Error(error);
  const current = bond
    ? bondNativeValue(
        { ...bond, minLinks: String(bond.minLinks ?? 'unknown') },
        advanced,
      )
    : 'object: absent';
  const candidate = bondNativeValue(draft, advanced);
  if (current === candidate)
    throw new Error('No Bond configuration changes to stage.');
  return {
    kind: 'bond',
    objectType: 'Port',
    objectName: draft.name,
    bridgeName: draft.bridge,
    title: `${bond ? 'Update' : 'Create'} Bond Port / ${draft.name}`,
    summary: `${draft.members.length} Interface members on ${draft.bridge} · ${draft.mode} · LACP ${draft.lacp}`,
    current,
    candidate,
    risk: draft.bridge === 'br-mgmt' || memberDown ? 'High' : 'Medium',
    capability: 'bond.manage',
    evidenceObject: `Port/${draft.name}`,
  };
}
