import test from 'node:test';
import assert from 'node:assert/strict';
import {
  capabilityCounts,
  capabilityStates,
  captureCapabilities,
  deriveCapabilities,
  filterCapabilities,
} from '../lib/capability-model.ts';
import {
  capabilityTtlMs,
  nativeFreshness,
  nativePolicyGates,
  nativeStageBlock,
  nativeTarget,
} from '../lib/native-capability.ts';
import {
  initialControlState,
  transition,
  transactionLocked,
  validationBlock,
} from '../lib/change-control.ts';
import { captureAcceleration } from '../lib/acceleration-model.ts';
import {
  collectFlowSnapshot,
  defaultFlowQuery,
} from '../lib/openflow-model.ts';
import { captureHealth, deriveHealth } from '../lib/health-model.ts';
import { ports } from '../lib/ovs-model.ts';

const now = Date.parse('2026-09-08T08:00:00Z');
const generation = initialControlState.generation;
const act = (state, type, extra = {}) =>
  transition(state, { type, now, ...extra });
const sample = (review = 'normal') =>
  captureCapabilities(review, now, generation);
const signals = () => ({
  acceleration: { snapshot: null, failure: null },
  openFlow: { snapshot: null, failure: null },
});
const observed = (review = 'normal') =>
  act(structuredClone(initialControlState), 'observe-native-capability', {
    proof: sample(review).proof,
  });
const stage = (state = observed(), extra = {}) =>
  act(state, 'stage-isolation', {
    desktop: true,
    impactAccepted: true,
    ...extra,
  });
const ready = () =>
  act(act(stage(), 'validate'), 'note', {
    note: 'Isolate protected demo peers',
  });
const active = () => act(ready(), 'start', { desktop: true });
const rows = (
  review = 'normal',
  time = now,
  state = observed(review),
  sources = signals(),
) => deriveCapabilities(sample(review), state, sources, time);
const native = (list) => list.find((row) => row.id === 'protected');

void test('native evidence yields all five availability states independently from permission and authority', () => {
  assert.deepEqual(capabilityStates, [
    'Enabled',
    'Available',
    'Missing prerequisites',
    'Unsupported',
    'Unknown',
  ]);
  for (const [review, expected] of [
    ['normal', 'Available'],
    ['enabled', 'Enabled'],
    ['missing', 'Missing prerequisites'],
    ['unsupported', 'Unsupported'],
    ['unknown', 'Unknown'],
    ['external', 'Available'],
    ['unauthorized', 'Available'],
    ['unsafe', 'Available'],
    ['degraded', 'Available'],
    ['unavailable', 'Unknown'],
    ['stale', 'Unknown'],
    ['generation-mismatch', 'Unknown'],
  ])
    assert.equal(native(rows(review)).state, expected, review);
  const uncertain = observed();
  uncertain.nativeCapability.prerequisites = null;
  assert.equal(native(rows('normal', now, uncertain)).state, 'Unknown');
  uncertain.nativeCapability.enabled = true;
  assert.equal(native(rows('normal', now, uncertain)).state, 'Unknown');
});

void test('four native gates require a current target, local NORMAL authority, permission and safe recovery', () => {
  const proof = sample().proof;
  assert.ok(
    nativePolicyGates(proof, 'normal', generation, now).every(
      (gate) => gate.passed,
    ),
  );
  for (const [patch, gate] of [
    [{ supported: null }, 'capability'],
    [{ canEnable: false }, 'capability'],
    [{ provider: 'Degraded' }, 'capability'],
    [{ prerequisites: false }, 'capability'],
    [{ target: 'Port/server-07' }, 'capability'],
    [{ bridge: 'br-fabric' }, 'capability'],
    [{ code: 'another-capability' }, 'capability'],
    [{ instance: 'another-instance' }, 'capability'],
    [{ authority: 'External controller' }, 'authority'],
    [{ normalSwitching: false }, 'authority'],
    [{ authorized: false }, 'authorization'],
    [{ validation: false }, 'safety'],
    [{ checkpoint: false }, 'safety'],
    [{ rollback: false }, 'safety'],
    [{ managementPath: 'Affected' }, 'safety'],
    [{ managementPath: 'Unknown' }, 'safety'],
    [{ highRisk: true, reauthenticated: false }, 'safety'],
    [{ impactReviewed: false }, 'safety'],
  ]) {
    const changed = { ...proof, ...patch };
    assert.equal(
      nativePolicyGates(changed, 'normal', generation, now).find(
        (item) => item.id === gate,
      ).passed,
      false,
    );
    assert.ok(stage({ ...observed(), nativeCapability: changed }).error);
  }
  assert.equal(
    stage({
      ...observed(),
      nativeCapability: { ...proof, highRisk: true, reauthenticated: true },
    }).error,
    null,
  );
});

void test('native proof expires at TTL and rejects future, invalid and mismatched observations', () => {
  const proof = sample().proof;
  assert.equal(
    nativeFreshness(proof, generation, now + capabilityTtlMs - 1),
    'Fresh',
  );
  assert.equal(
    nativeFreshness(proof, generation, now + capabilityTtlMs),
    'Stale',
  );
  assert.equal(nativeFreshness(proof, generation, now - 1), 'Stale');
  assert.equal(
    nativeFreshness({ ...proof, observedAt: NaN }, generation, now),
    'Stale',
  );
  assert.equal(nativeFreshness(proof, generation, NaN), 'Stale');
  assert.equal(
    nativeFreshness(proof, generation + 1, now),
    'Generation mismatch',
  );
  assert.equal(nativeFreshness(null, generation, now), 'Unavailable');
  const snapshot = { ...sample(), observedAt: NaN };
  assert.equal(
    deriveCapabilities(snapshot, observed(), signals(), now).find(
      (row) => row.id === 'bridge',
    ).state,
    'Unknown',
  );
});

void test('native staging requires desktop, an explicit impact acknowledgment and an empty Candidate', () => {
  for (const extra of [
    { desktop: false },
    { impactAccepted: false },
    { impactAccepted: undefined },
  ])
    assert.equal(stage(observed(), extra).candidate, null);
  const vlan = act(observed(), 'stage', {
    desktop: true,
    port: ports[1],
    mine: { mode: 'trunk', tag: null, trunks: '120, 240' },
  });
  assert.equal(stage(vlan).candidate.kind, 'vlan');
  assert.match(stage(vlan).error, /existing Candidate/);
  for (const scenario of [
    'permission-denied',
    'provider-unavailable',
    'degraded',
    'stale',
    'empty',
    'drift',
    'conflict',
  ])
    assert.ok(stage({ ...observed(), scenario }).error, scenario);
  assert.ok(stage(observed('enabled')).error);
});

void test('staging native intent changes no running configuration and records the isolated target', () => {
  const state = stage();
  assert.equal(state.error, null);
  assert.equal(state.candidate.kind, 'isolation');
  assert.equal(state.candidate.intent.objectType, 'Port');
  assert.equal(state.candidate.intent.evidenceObject, nativeTarget.target);
  assert.equal(state.candidate.intent.bridgeName, nativeTarget.bridge);
  assert.equal(state.nativeEnabled ?? false, false);
  assert.equal(state.nativeCapability.enabled, false);
  assert.deepEqual(state.live, initialControlState.live);
  assert.equal(state.transaction.status, 'idle');
  assert.ok(
    state.evidence.some(
      (entry) =>
        entry.kind === 'Audit' &&
        entry.text.includes('Running configuration is unchanged'),
    ),
  );
});

void test('native validation uses action time and cannot reuse validation after an observation changes', () => {
  const state = stage();
  const valid = act(state, 'validate');
  assert.equal(valid.validatedRevision, state.candidate.revision);
  assert.equal(validationBlock(valid, now), null);
  assert.ok(act(state, 'validate', { now: now + capabilityTtlMs }).error);
  const changed = act(valid, 'observe-native-capability', {
    proof: sample('external').proof,
  });
  assert.equal(changed.validatedRevision, null);
  assert.ok(act(changed, 'validate').error);
  assert.ok(act(changed, 'start', { desktop: true }).error);
});

void test('native Safe Apply requires validation and Audit reason; provisional state confirms through the shared Job', () => {
  assert.ok(act(stage(), 'start', { desktop: true }).error);
  assert.ok(act(act(stage(), 'validate'), 'start', { desktop: true }).error);
  assert.ok(act(ready(), 'start', { desktop: false }).error);
  const pending = active();
  assert.equal(pending.transaction.status, 'countdown');
  assert.equal(pending.nativeEnabled, true);
  assert.equal(pending.nativeCapability.enabled, true);
  assert.match(pending.nativeCapability.source, /provisional-apply/);
  assert.ok(transactionLocked(pending.transaction.status));
  assert.ok(stage(pending).error);
  assert.ok(act(pending, 'start', { desktop: true }).error);
  const done = act(pending, 'confirm', { now: now + 1000 });
  assert.equal(done.transaction.status, 'confirmed');
  assert.equal(done.nativeEnabled, true);
  assert.equal(done.candidate, null);
  assert.equal(done.generation, generation + 1);
  assert.equal(done.nativeCapability.generation, done.generation);
  assert.ok(
    done.evidence.some(
      (entry) =>
        entry.kind === 'Audit' &&
        entry.correlation === pending.transaction.correlation,
    ),
  );
  const snapshot = captureCapabilities(
    'normal',
    now + 1001,
    done.generation,
    done.nativeEnabled,
  );
  const current = act(done, 'observe-native-capability', {
    proof: snapshot.proof,
  });
  assert.equal(
    native(deriveCapabilities(snapshot, current, signals(), now + 1001)).state,
    'Enabled',
  );
  assert.ok(
    stage(current, { now: now + 1001 }).error,
    'Repeated enable must not create another intent',
  );
});

void test('native authority and safety are rechecked at validate, submit and confirm', () => {
  for (const patch of [
    { authorized: false },
    { authority: 'Unknown' },
    { normalSwitching: false },
    { provider: 'Degraded' },
    { checkpoint: false },
    { rollback: false },
    { highRisk: true },
    { managementPath: 'Affected' },
    { canEnable: false },
    { observedAt: now - capabilityTtlMs },
    { generation: generation - 1 },
  ]) {
    for (const { base, action, extra } of [
      { base: stage(), action: 'validate', extra: {} },
      { base: ready(), action: 'start', extra: { desktop: true } },
      { base: active(), action: 'confirm', extra: {} },
    ]) {
      const changed = {
        ...base,
        nativeCapability: { ...base.nativeCapability, ...patch },
      };
      const next = act(changed, action, extra);
      assert.ok(next.error, `${action}: ${JSON.stringify(patch)}`);
      assert.equal(next.transaction.status, base.transaction.status);
    }
  }
  assert.ok(
    act(
      {
        ...active(),
        nativeCapability: { ...active().nativeCapability, enabled: false },
      },
      'confirm',
    ).error,
  );
});

void test('manual rollback compares this transaction target and preserves unexpected external changes', () => {
  const pending = active();
  for (const patch of [
    { target: 'Port/server-07' },
    { bridge: 'br-fabric' },
    { code: 'another-code' },
    { enabled: false },
    { enabled: null },
    { authority: 'External controller' },
    { authorized: false },
    { provider: 'Unavailable' },
    { rollback: false },
    { observedAt: now - capabilityTtlMs },
  ]) {
    const next = act(
      {
        ...pending,
        nativeCapability: { ...pending.nativeCapability, ...patch },
      },
      'rollback',
    );
    assert.ok(next.error, JSON.stringify(patch));
    assert.equal(next.nativeEnabled, true);
    assert.equal(next.transaction.status, 'countdown');
  }
  const restored = act(pending, 'rollback');
  assert.equal(restored.transaction.status, 'rolled-back');
  assert.equal(restored.nativeEnabled, false);
  assert.equal(restored.nativeCapability.enabled, false);
  assert.equal(restored.candidate.kind, 'isolation');
  assert.equal(restored.validatedRevision, null);
  assert.match(restored.nativeCapability.source, /compare-before-rollback/);
});

void test('deadline recovery is manager owned but cannot roll back using stale or mismatched evidence', () => {
  const pending = active();
  const deadline = pending.transaction.deadline;
  const expired = act(pending, 'tick', { now: deadline });
  assert.equal(expired.transaction.status, 'rollback-conflict');
  assert.equal(expired.nativeEnabled, true);
  assert.ok(transactionLocked(expired.transaction.status));
  const fresh = {
    ...pending,
    nativeCapability: {
      ...pending.nativeCapability,
      observedAt: deadline - 1,
      authorized: false,
    },
  };
  assert.ok(act(fresh, 'rollback', { now: deadline - 1 }).error);
  const restored = act(fresh, 'tick', { now: deadline });
  assert.equal(restored.transaction.status, 'rolled-back');
  assert.equal(restored.nativeEnabled, false);
  const mismatch = {
    ...fresh,
    nativeCapability: { ...fresh.nativeCapability, target: 'Port/other' },
  };
  assert.equal(
    act(mismatch, 'tick', { now: deadline }).transaction.status,
    'rollback-conflict',
  );
});

void test('native OutcomeUnknown reconciliation retains the original Job and never reissues a write', () => {
  const pending = active();
  const uncertain = act(pending, 'scenario', { scenario: 'outcome-unknown' });
  assert.equal(uncertain.transaction.status, 'outcome-unknown');
  assert.ok(stage(uncertain).error);
  for (const result of [
    'applied',
    'not-applied',
    'degraded',
    'needs-attention',
  ]) {
    const reconciled = act(uncertain, 'reconcile', { result });
    assert.equal(reconciled.sequence, pending.sequence);
    assert.equal(reconciled.transaction.id, pending.transaction.id);
    assert.equal(
      reconciled.transaction.correlation,
      pending.transaction.correlation,
    );
    if (['applied', 'not-applied'].includes(result)) {
      assert.equal(reconciled.nativeEnabled, result === 'applied');
      assert.equal(
        reconciled.nativeCapability,
        null,
        'Fresh observations are needed after reconciliation',
      );
    } else assert.ok(transactionLocked(reconciled.transaction.status));
  }
});

void test('global DPDK and offload rows reuse the accepted evidence model and stay Observe for every availability', () => {
  for (const [review, expected] of [
    ['enabled', 'Enabled'],
    ['available', 'Available'],
    ['missing', 'Missing prerequisites'],
    ['unsupported', 'Unsupported'],
    ['unknown', 'Unknown'],
  ]) {
    const sources = signals();
    sources.acceleration.snapshot = captureAcceleration(
      review,
      now,
      generation,
    );
    const list = rows('normal', now, observed(), sources);
    for (const id of ['dpdk', 'offload']) {
      const row = list.find((item) => item.id === id);
      assert.equal(row.state, expected);
      assert.equal(row.level, 'Observe');
      assert.equal(row.canEnable, false);
      assert.equal(row.source, sources.acceleration.snapshot.id);
      assert.equal(row.observedAt, now);
    }
  }
});

void test('refreshing the registry cannot renew independent acceleration or OpenFlow evidence', () => {
  for (const row of rows().filter((item) =>
    ['dpdk', 'offload', 'openflow'].includes(item.id),
  )) {
    assert.equal(row.providerState, 'Unavailable');
    assert.equal(row.state, 'Unknown');
  }
  const sources = signals();
  sources.acceleration.snapshot = captureAcceleration(
    'enabled',
    now,
    generation,
  );
  sources.openFlow.snapshot = collectFlowSnapshot(
    defaultFlowQuery,
    'normal',
    now,
  ).snapshot;
  const later = now + capabilityTtlMs;
  const snapshot = captureCapabilities('normal', later, generation);
  const current = act(observed(), 'observe-native-capability', {
    proof: snapshot.proof,
  });
  const list = deriveCapabilities(snapshot, current, sources, later);
  assert.equal(native(list).state, 'Available');
  for (const id of ['dpdk', 'offload', 'openflow']) {
    const row = list.find((item) => item.id === id);
    assert.equal(row.state, 'Unknown');
    assert.equal(row.freshness, 'Stale');
    assert.equal(row.observedAt, now);
    assert.equal(row.canEnable, false);
  }
  assert.equal(list.find((item) => item.id === 'openflow').generation, null);
});

void test('permission denial redacts the catalog; empty, failed and filtered results remain distinct', () => {
  const sources = signals();
  sources.acceleration.snapshot = captureAcceleration(
    'enabled',
    now,
    generation,
  );
  assert.deepEqual(
    rows(
      'normal',
      now,
      { ...observed(), scenario: 'permission-denied' },
      sources,
    ),
    [],
  );
  const empty = rows('empty', now, observed('empty'), sources);
  assert.equal(native(empty).state, 'Unknown');
  assert.equal(empty.find((row) => row.id === 'dpdk').state, 'Enabled');
  const failed = deriveCapabilities(sample(), observed(), sources, now, true);
  assert.equal(native(failed).providerState, 'Unavailable');
  assert.equal(failed.find((row) => row.id === 'bridge').state, 'Unknown');
  assert.equal(failed.find((row) => row.id === 'dpdk').state, 'Enabled');
  const list = rows();
  assert.equal(list.length, 9);
  assert.equal(
    Object.values(capabilityCounts(list)).reduce(
      (sum, count) => sum + count,
      0,
    ),
    list.length,
  );
  assert.deepEqual(
    filterCapabilities(
      list,
      'OVS.PORT.PROTECTED',
      'Available',
      'Switching',
    ).map((row) => row.id),
    ['protected'],
  );
  assert.deepEqual(filterCapabilities(list, 'not-present', 'all', 'all'), []);
});

void test('Health uses the shared native source and distinguishes provider health from capability availability', () => {
  for (const [review, status] of [
    ['normal', 'Healthy'],
    ['unsupported', 'Healthy'],
    ['degraded', 'Degraded'],
    ['stale', 'Unknown'],
  ]) {
    const state = observed(review);
    const list = rows(review);
    const health = deriveHealth(
      captureHealth('healthy', now, generation),
      state,
      {
        ...signals(),
        diagnostic: {
          state: 'not-started',
          scope: nativeTarget.target,
          reviewOnly: false,
        },
        capability: { rows: list, failure: null },
      },
      now,
    );
    const component = health.components.find(
      (row) => row.id === 'native-capability-provider',
    );
    assert.equal(component.status, status);
    assert.equal(component.observedAt, native(list).observedAt);
    assert.equal(component.source, native(list).source);
    assert.match(component.reason, new RegExp(native(list).state));
    assert.equal(component.action.view, 'capabilities');
    assert.match(component.impact, /does not.*prove forwarding health/);
  }
});

void test('uncertain and active shared transactions block native staging even when every gate passes', () => {
  for (const status of [
    'countdown',
    'outcome-unknown',
    'rollback-conflict',
    'degraded',
    'needs-attention',
  ])
    assert.match(
      nativeStageBlock(sample().proof, 'normal', generation, now, true, status),
      /existing transaction/,
    );
});
