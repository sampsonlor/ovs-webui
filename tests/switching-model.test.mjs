import test from 'node:test';
import assert from 'node:assert/strict';
import {
  bridges,
  bonds,
  bridgeObservation,
  bondObservation,
  bondDraftErrors,
  bondChangeIntent,
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

void test('Bond creation shares the editor mapping, free members and minimum links across entry points', () => {
  const input = {
    name: draft.name,
    bridge: draft.bridge,
    mode: draft.mode,
    lacp: draft.lacp,
  };
  assert.deepEqual(representativeBondIntent(input), bondChangeIntent(draft));
  assert.deepEqual(
    representativeBondIntent({
      ...input,
      members: [...draft.members].reverse(),
      minLinks: 2,
    }),
    bondChangeIntent({ ...draft, minLinks: '2' }),
  );
  assert.deepEqual(
    representativeBondIntent(input, true),
    bondChangeIntent(draft, undefined, false, true),
  );
});

void test('programmatic creation cannot claim occupied Interfaces or reuse a Port from either inventory', () => {
  const input = {
    name: draft.name,
    bridge: draft.bridge,
    mode: draft.mode,
    lacp: draft.lacp,
  };
  for (const name of [
    'uplink-01',
    'server-07',
    'bond-uplink',
    'bond-storage',
    'mgmt0',
  ]) {
    assert.ok(bondDraftErrors({ ...draft, name }).name);
    assert.throws(
      () => representativeBondIntent({ ...input, name }),
      /already exists/,
    );
  }
  for (const bridge of ['br-storage', 'br-mgmt'])
    assert.throws(
      () => representativeBondIntent({ ...input, bridge }),
      /at least two/,
    );
  for (const members of [
    uplink.members,
    ['enp65s0f2'],
    ['enp65s0f2', 'enp65s0f2'],
    ['unknown', 'enp65s0f3'],
  ]) {
    assert.ok(bondDraftErrors({ ...draft, members }).members);
    assert.throws(() => representativeBondIntent({ ...input, members }));
  }
});

void test('programmatic Bond inputs reject malformed members, policies and out-of-range minimum links', () => {
  const input = {
    name: draft.name,
    bridge: draft.bridge,
    mode: draft.mode,
    lacp: draft.lacp,
  };
  for (const patch of [
    { members: null },
    { members: 'enp65s0f2,enp65s0f3' },
    { members: [1, 2] },
    { minLinks: '' },
    { minLinks: null },
    { minLinks: -1 },
    { minLinks: 3 },
    { minLinks: 0.5 },
    { minLinks: Infinity },
    { mode: 'unknown' },
    { lacp: 'off' },
  ])
    assert.throws(() => representativeBondIntent({ ...input, ...patch }));
  assert.ok(bondDraftErrors({ ...draft, mode: 'unknown' }).policy);
  assert.ok(bondDraftErrors({ ...draft, lacp: 'unknown' }).policy);
});

void test('shared Bond updates preserve advanced fields and reject rename, reparent, no-op and Observe writes', () => {
  const existing = { ...uplink, minLinks: '1' };
  assert.throws(() => bondChangeIntent(existing, uplink), /No Bond/);
  assert.throws(
    () => bondChangeIntent({ ...existing, name: 'renamed' }, uplink),
    /name cannot/,
  );
  assert.throws(
    () => bondChangeIntent({ ...existing, bridge: 'br-storage' }, uplink),
    /cannot move/,
  );
  assert.throws(
    () => bondChangeIntent({ ...provider, minLinks: '1' }, provider),
    /Observe/,
  );
  const intent = bondChangeIntent({ ...existing, minLinks: '2' }, uplink, true);
  assert.match(intent.current, /other_config:min-links: 1/);
  assert.match(intent.candidate, /other_config:min-links: 2/);
  assert.match(intent.current, /bond-rebalance-interval: 10000/);
  assert.match(intent.candidate, /bond-rebalance-interval: 10000/);
  assert.equal(intent.evidenceObject, 'Port/bond-uplink');
});

void test('provider-owned member evidence remains unknown in normal and degraded snapshots', () => {
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

void test('active-backup uses active capacity and does not present standby or down members as forwarding', () => {
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
  assert.deepEqual(storage.members, ['enp130s0f0', 'enp130s0f1']);
});

void test('LACP mismatch withholds forwarding capacity and traffic while retaining link evidence', () => {
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

void test('missing speed does not become zero aggregate capacity', () => {
  const partial = bondObservation(
    { ...uplink, memberSpeeds: [100, null] },
    'normal',
  );
  assert.equal(partial.capacity, 'Unknown');
  assert.equal(partial.members[1].speed, 'Unknown');
});

void test('Bridge posture follows only its own Bond evidence and keeps external RSTP unknown', () => {
  assert.equal(bridgeObservation(bridges[0], 'member-down').state, 'Up');
  assert.equal(bridgeObservation(bridges[1], 'member-down').state, 'Degraded');
  assert.equal(
    bridgeObservation(bridges[0], 'lacp-mismatch').state,
    'Degraded',
  );
  assert.equal(bridgeObservation(bridges[3], 'normal').state, 'Unknown');
  assert.equal(bridges[3].rstp, 'Unknown');
});

void test('Bond editor distinguishes owned members from Interfaces already assigned to another Port', () => {
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

void test('Bond editor reports concrete name, membership, LACP and minimum-link constraints', () => {
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

void test('the displayed global staging block agrees with the shared transition guard', () => {
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
