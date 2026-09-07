import test from 'node:test';
import assert from 'node:assert/strict';
import {
  bridges,
  bonds,
  bridgeObservation,
  bondObservation,
  bondDraftErrors,
  memberOptions,
} from '../lib/switching-model.ts';
import {
  initialControlState,
  topologyStageBlock,
  transition,
} from '../lib/change-control.ts';
import { representativeBondIntent } from '../lib/p1-control.ts';

const uplink = bonds.find((bond) => bond.name === 'bond-uplink');
const storage = bonds.find((bond) => bond.name === 'bond-storage');
const provider = bonds.find((bond) => bond.name === 'bond-provider');
const draft = {
  name: 'bond-review',
  bridge: 'br-fabric',
  mode: 'balance-tcp',
  lacp: 'active',
  minLinks: '1',
  members: ['enp65s0f2', 'enp65s0f3'],
};

test('provider-owned member evidence remains unknown in normal and degraded snapshots', () => {
  for (const scenario of ['normal', 'degraded', 'provider-degraded']) {
    const observed = bondObservation(provider, scenario);
    assert.equal(observed.state, 'Unknown');
    assert.equal(observed.capacity, 'Unknown');
    assert.equal(observed.negotiation, 'Unknown');
    for (const member of observed.members)
      for (const field of ['link', 'speed', 'role', 'lacp', 'traffic'])
        assert.equal(member[field], 'Unknown');
    assert.equal(
      observed.freshness,
      scenario === 'normal'
        ? 'Evidence unavailable'
        : 'Stale provider evidence',
    );
  }
  assert.equal(provider.minLinks, null);
});

test('active-backup uses active capacity and does not present standby or down members as forwarding', () => {
  const normal = bondObservation(storage, 'normal');
  assert.equal(normal.capacity, '25 Gbps active');
  assert.equal(normal.members[1].role, 'Standby');
  assert.equal(normal.members[1].traffic, '0 bps');
  const degraded = bondObservation(storage, 'member-down');
  assert.equal(degraded.state, 'Degraded');
  assert.equal(degraded.members[0].role, 'Active');
  assert.equal(degraded.members[1].link, 'Down');
  assert.equal(degraded.members[1].role, 'Inactive');
  assert.equal(degraded.capacity, '25 Gbps active');
  assert.deepEqual(storage.members, ['enp129s0f0', 'enp129s0f1']);
});

test('LACP mismatch withholds forwarding capacity and traffic while retaining link evidence', () => {
  const normal = bondObservation(uplink, 'normal');
  assert.equal(normal.capacity, '200 Gbps aggregate');
  assert.equal(normal.negotiation, 'Negotiated');
  const mismatch = bondObservation(uplink, 'lacp-mismatch');
  assert.equal(mismatch.state, 'Degraded');
  assert.equal(mismatch.capacity, 'Unverified');
  assert.equal(mismatch.negotiation, 'Partner mismatch');
  assert.deepEqual(
    mismatch.members.map((member) => member.role),
    ['Collecting only', 'Detached'],
  );
  assert.ok(
    mismatch.members.every(
      (member) => member.link === 'Up' && member.traffic === 'Unknown',
    ),
  );
});

test('missing speed does not become zero aggregate capacity', () => {
  const partial = bondObservation(
    { ...uplink, memberSpeeds: [100, null] },
    'normal',
  );
  assert.equal(partial.capacity, 'Unknown');
  assert.equal(partial.members[1].speed, 'Unknown');
});

test('Bridge posture follows only its own Bond evidence and keeps external RSTP unknown', () => {
  assert.equal(bridgeObservation(bridges[0], 'member-down').state, 'Up');
  assert.equal(bridgeObservation(bridges[1], 'member-down').state, 'Degraded');
  assert.equal(
    bridgeObservation(bridges[0], 'lacp-mismatch').state,
    'Degraded',
  );
  assert.equal(bridgeObservation(bridges[3], 'normal').state, 'Unknown');
  assert.equal(bridges[3].rstp, 'Unknown');
});

test('Bond editor distinguishes owned members from Interfaces already assigned to another Port', () => {
  assert.ok(
    Object.values(bondDraftErrors(draft)).every((value) => value === null),
  );
  assert.ok(bondDraftErrors({ ...draft, name: 'server-07' }).name);
  assert.ok(bondDraftErrors({ ...draft, name: 'bond-uplink' }).name);
  assert.ok(bondDraftErrors({ ...draft, members: uplink.members }).members);
  assert.ok(memberOptions('br-storage').every((option) => !option.available));
  assert.ok(
    Object.values(
      bondDraftErrors(
        {
          name: storage.name,
          bridge: storage.bridge,
          mode: storage.mode,
          lacp: storage.lacp,
          minLinks: '1',
          members: storage.members,
        },
        storage,
      ),
    ).every((value) => value === null),
  );
  assert.ok(bondDraftErrors({ ...draft, bridge: 'br-storage' }).members);
});

test('Bond editor reports concrete name, membership, LACP and minimum-link constraints', () => {
  assert.ok(bondDraftErrors({ ...draft, name: 'invalid name' }).name);
  assert.ok(bondDraftErrors({ ...draft, members: ['enp65s0f2'] }).members);
  assert.ok(
    bondDraftErrors({ ...draft, members: ['enp65s0f2', 'enp65s0f2'] }).members,
  );
  assert.ok(bondDraftErrors({ ...draft, lacp: 'off' }).policy);
  for (const minLinks of ['', '0.5', '-1', '3'])
    assert.ok(bondDraftErrors({ ...draft, minLinks }).minLinks);
  assert.ok(bondDraftErrors({ ...draft, bridge: 'br-offload' }).bridge);
});

test('the displayed global staging block agrees with the shared transition guard', () => {
  const intent = representativeBondIntent({
    name: 'bond-review',
    bridge: 'br-fabric',
    mode: 'balance-tcp',
    lacp: 'active',
  });
  for (const scenario of [
    'normal',
    'permission-denied',
    'provider-degraded',
    'network-loss',
  ]) {
    for (const desktop of [true, false]) {
      const state = { ...structuredClone(initialControlState), scenario };
      assert.equal(
        transition(state, {
          type: 'stage-topology',
          intent,
          desktop,
          now: 1000,
        }).error,
        topologyStageBlock(state, desktop),
      );
    }
  }
  const unknown = {
    ...structuredClone(initialControlState),
    transaction: {
      ...initialControlState.transaction,
      status: 'outcome-unknown',
    },
  };
  assert.match(topologyStageBlock(unknown, true), /active transaction/);
});
