import test from 'node:test';
import assert from 'node:assert/strict';
import {
  initialControlState,
  transition,
  applyBlock,
  sameVlan,
  validateVlan,
  transactionLocked,
} from '../lib/change-control.ts';
import { ports } from '../lib/ovs-model.ts';

const target = ports[1];
const mine = { mode: 'trunk', tag: null, trunks: '120, 240' };
const now = 100_000;
const action = (state, type, extra = {}) =>
  transition(state, { type, now, ...extra });
function staged(port = target) {
  return action(structuredClone(initialControlState), 'stage', {
    port,
    mine,
    desktop: true,
  });
}
function ready() {
  let state = staged();
  state = action(state, 'validate');
  return action(state, 'note', { note: 'Add storage VLAN' });
}
function active() {
  return action(ready(), 'start', { desktop: true });
}

test('empty, unvalidated, reasonless and narrow-screen requests cannot start transactions', () => {
  assert.match(
    action(initialControlState, 'start', { desktop: true }).error,
    /empty/,
  );
  assert.match(action(staged(), 'start', { desktop: true }).error, /Validate/);
  assert.match(
    action(action(staged(), 'validate'), 'start', { desktop: true }).error,
    /reason/,
  );
  assert.match(action(ready(), 'start', { desktop: false }).error, /desktop/);
  assert.equal(active().transaction.status, 'countdown');
});

test('idle and completed jobs never accept confirm or rollback', () => {
  for (const decision of ['confirm', 'rollback'])
    assert.ok(action(initialControlState, decision).error);
  const done = action(active(), 'confirm');
  for (const decision of ['confirm', 'rollback'])
    assert.ok(action(done, decision).error);
  assert.equal(done.candidate, null);
  assert.deepEqual(done.live[target.name], mine);
});

test('read-only authority, unavailable permission and mobile cannot stage', () => {
  assert.match(staged(ports[4]).error, /Observe/);
  const denied = action(initialControlState, 'scenario', {
    scenario: 'permission-denied',
  });
  assert.ok(
    action(denied, 'stage', { port: target, mine, desktop: true }).error,
  );
  assert.ok(
    action(initialControlState, 'stage', { port: target, mine, desktop: false })
      .error,
  );
});

test('VLAN input rejects malformed and out-of-range intent and preserves native semantics', () => {
  for (const trunks of ['0', '4095', '40-10', '1,,2', 'a', '1-', ''])
    assert.ok(validateVlan({ ...mine, trunks }));
  assert.ok(validateVlan({ ...mine, tag: 120 }));
  assert.ok(validateVlan({ mode: 'access', tag: 12.5, trunks: '' }));
  assert.ok(validateVlan({ mode: 'access', tag: 120, trunks: '240' }));
  assert.equal(validateVlan({ ...mine, trunks: '1, 10-20, 4094' }), null);
  assert.ok(
    sameVlan(
      { ...mine, trunks: '120, 240-241' },
      { ...mine, trunks: '241, 240,120,120' },
    ),
  );
});

test('candidate captures the selected object and one intent cannot silently replace another', () => {
  const state = staged(ports[2]);
  assert.equal(state.candidate.port.name, 'server-08');
  assert.deepEqual(state.candidate.mine, mine);
  assert.ok(
    action(state, 'stage', { port: target, mine, desktop: true }).error,
  );
  assert.equal(
    action(state, 'stage', { port: target, mine, desktop: true }).candidate.port
      .name,
    'server-08',
  );
});

test('active transaction rejects restaging, discarding and duplicate submissions', () => {
  const state = active();
  for (const attempt of [
    action(state, 'start', { desktop: true }),
    action(state, 'discard'),
    action(state, 'stage', { port: target, mine, desktop: true }),
  ]) {
    assert.ok(attempt.error);
    assert.equal(attempt.sequence, 1);
    assert.equal(attempt.transaction.id, state.transaction.id);
  }
});

test('conflict requires explicit choice; using mine rebases but never applies', () => {
  const conflict = action(ready(), 'scenario', { scenario: 'conflict' });
  assert.ok(action(conflict, 'start', { desktop: true }).error);
  assert.ok(action(conflict, 'rebase', { choice: 'non-overlapping' }).error);
  const rebased = action(conflict, 'rebase', { choice: 'mine' });
  assert.equal(rebased.candidate.base.tag, 130);
  assert.deepEqual(rebased.candidate.mine, mine);
  assert.equal(rebased.candidate.baseGeneration, rebased.generation);
  assert.equal(rebased.validatedRevision, null);
  assert.equal(rebased.live[target.name].tag, 130);
  assert.match(applyBlock(rebased), /Validate/);
});

test('keeping current removes only candidate intent and records the current external value', () => {
  const state = action(
    action(staged(), 'scenario', { scenario: 'conflict' }),
    'rebase',
    { choice: 'current' },
  );
  assert.equal(state.candidate, null);
  assert.equal(state.live[target.name].tag, 130);
  assert.equal(state.transaction.status, 'idle');
});

test('stale non-overlapping merge preserves intent; drift is a separate read-only action', () => {
  const state = action(ready(), 'scenario', { scenario: 'stale' });
  const rebased = action(state, 'rebase', { choice: 'non-overlapping' });
  assert.deepEqual(rebased.candidate.base, state.candidate.base);
  assert.deepEqual(rebased.candidate.mine, state.candidate.mine);
  assert.equal(rebased.validatedRevision, null);
  const drift = action(ready(), 'scenario', { scenario: 'drift' });
  assert.ok(action(drift, 'rebase', { choice: 'mine' }).error);
  const reconciled = action(drift, 'inspect-drift');
  assert.deepEqual(reconciled.candidate, drift.candidate);
  assert.deepEqual(reconciled.live, drift.live);
  assert.equal(reconciled.validatedRevision, null);
});

test('unknown result stays locked even if the review selector returns to normal', () => {
  let state = action(active(), 'scenario', { scenario: 'outcome-unknown' });
  state = action(state, 'scenario', { scenario: 'normal' });
  assert.equal(state.transaction.status, 'outcome-unknown');
  assert.ok(action(state, 'confirm').error);
  assert.ok(action(state, 'rollback').error);
  assert.ok(action(state, 'start', { desktop: true }).error);
});

test('all four reconciliation outcomes use the original job without resubmission', () => {
  const unknown = action(active(), 'scenario', { scenario: 'outcome-unknown' });
  for (const result of [
    'applied',
    'not-applied',
    'degraded',
    'needs-attention',
  ]) {
    const state = action(unknown, 'reconcile', { result });
    assert.equal(state.transaction.id, unknown.transaction.id);
    assert.equal(state.sequence, 1);
    assert.equal(state.transaction.status, result);
    assert.equal(
      transactionLocked(result),
      ['degraded', 'needs-attention'].includes(result),
    );
    assert.equal(state.candidate === null, result === 'applied');
    assert.equal(state.validatedRevision, null);
  }
});

test('deadline uses elapsed time, not interval count; disconnected expiry never claims success', () => {
  const state = active();
  assert.equal(
    action(state, 'tick', { now: now + 89_999 }).transaction.status,
    'countdown',
  );
  assert.equal(
    action(state, 'tick', { now: now + 90_000 }).transaction.status,
    'rolled-back',
  );
  const disconnected = action(state, 'scenario', { scenario: 'network-loss' });
  assert.ok(action(disconnected, 'confirm').error);
  assert.ok(action(disconnected, 'rollback').error);
  const expired = action(disconnected, 'tick', { now: now + 200_000 });
  assert.equal(expired.transaction.status, 'outcome-unknown');
  assert.ok(action(expired, 'reconcile', { result: 'applied' }).error);
  assert.equal(
    action(expired, 'reconnect', { now: now + 200_000 }).transaction.status,
    'outcome-unknown',
  );
});

test('late confirm cannot beat expiry and external writes stop protected rollback', () => {
  const state = active();
  assert.equal(
    action(state, 'confirm', { now: now + 90_001 }).transaction.status,
    'rolled-back',
  );
  const external = action(state, 'scenario', { scenario: 'rollback-conflict' });
  const stopped = action(external, 'rollback');
  assert.equal(stopped.transaction.status, 'rollback-conflict');
  assert.deepEqual(stopped.live, external.live);
  assert.equal(stopped.generation, external.generation);
  assert.ok(action(stopped, 'start', { desktop: true }).error);
});

test('readiness is invalidated when an already-validated candidate changes', () => {
  const state = action(ready(), 'stage', {
    port: target,
    mine: { ...mine, trunks: '120, 300' },
    desktop: true,
  });
  assert.equal(state.candidate.revision, 2);
  assert.equal(state.validatedRevision, null);
  assert.match(applyBlock(state), /Validate/);
});
