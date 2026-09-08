import test from 'node:test';
import assert from 'node:assert/strict';
import {
  initialControlState,
  transactionLocked,
} from '../lib/change-control.ts';
import {
  captureHealth,
  deriveHealth,
  healthEvents,
  healthReadBlock,
  healthTtlMs,
  preserveHealthSince,
  rollupHealth,
} from '../lib/health-model.ts';
import { captureAcceleration } from '../lib/acceleration-model.ts';
import {
  collectFlowSnapshot,
  defaultFlowQuery,
} from '../lib/openflow-model.ts';

const now = Date.parse('2026-09-08T02:00:00Z');
const generation = initialControlState.generation;
const control = () => structuredClone(initialControlState);
const signals = () => ({
  openFlow: { snapshot: null, failure: null },
  acceleration: { snapshot: null, failure: null },
  diagnostic: {
    state: 'not-started',
    scope: 'Port/bond-storage',
    reviewOnly: false,
  },
});
const snapshot = (review = 'healthy') => captureHealth(review, now, generation);
const derive = (
  review = 'healthy',
  state = control(),
  observations = signals(),
  time = now,
  failed = false,
) => deriveHealth(snapshot(review), state, observations, time, failed);
const component = (health, id) =>
  health.components.find((row) => row.id === id);

for (const [review, expected] of [
  ['healthy', 'Healthy'],
  ['degraded', 'Degraded'],
  ['critical', 'Critical'],
  ['unknown', 'Unknown'],
  ['recovering', 'Recovering'],
  ['recovery-required', 'Recovery Required'],
]) {
  test(`health sample explains ${expected} with component evidence`, () => {
    const health = derive(review);
    assert.equal(health.overall, expected);
    assert.ok(
      health.components.every(
        (row) => row.source && row.reason && row.impact && row.action.label,
      ),
    );
    assert.ok(health.components.every((row) => row.freshness === 'Fresh'));
  });
}

test('confirmed severity and recovery retain priority while unknown coverage stays explicit', () => {
  assert.equal(rollupHealth([]), 'Unknown');
  assert.equal(
    rollupHealth([{ status: 'Healthy' }, { status: 'Unknown' }]),
    'Unknown',
  );
  assert.equal(
    rollupHealth([{ status: 'Critical' }, { status: 'Unknown' }]),
    'Critical',
  );
  assert.equal(
    rollupHealth([{ status: 'Critical' }, { status: 'Recovery Required' }]),
    'Recovery Required',
  );
  const health = derive('normal');
  assert.equal(health.overall, 'Degraded');
  assert.equal(health.unknown, 1);
  assert.equal(component(health, 'link').related, 'Port/server-08');
});

test('management failure does not imply datapath failure or create a replacement database', () => {
  const api = derive('management-critical');
  assert.equal(component(api, 'api').status, 'Critical');
  assert.equal(component(api, 'packet-path').status, 'Healthy');
  const recovery = derive('recovery-required');
  assert.equal(component(recovery, 'manager-db').status, 'Recovery Required');
  assert.equal(component(recovery, 'packet-path').status, 'Healthy');
  assert.equal(component(recovery, 'manager-db').action.view, 'evidence');
  assert.match(
    component(recovery, 'manager-db').value,
    /do not create an empty/,
  );
});

test('daemon failure leaves forwarding unknown rather than guessing a datapath outage', () => {
  const health = derive('critical');
  assert.equal(component(health, 'vswitchd').status, 'Critical');
  assert.equal(component(health, 'packet-path').status, 'Unknown');
});

test('health expires at its exact TTL and rejects future or mismatched-generation observations', () => {
  assert.equal(
    derive('healthy', control(), signals(), now + healthTtlMs - 1).overall,
    'Healthy',
  );
  for (const expired of [
    derive('healthy', control(), signals(), now + healthTtlMs),
    derive('healthy', control(), signals(), now - 1),
    derive('generation-mismatch'),
    derive('stale'),
  ]) {
    assert.equal(expired.overall, 'Unknown');
    assert.equal(component(expired, 'ovsdb').freshness, 'Stale');
    assert.equal(component(expired, 'ovsdb').historicalStatus, 'Healthy');
  }
});

test('failed refresh retains historical findings and cannot reassert current Healthy', () => {
  const health = derive('critical', control(), signals(), now, true);
  assert.equal(health.overall, 'Unknown');
  assert.equal(component(health, 'vswitchd').historicalStatus, 'Critical');
  assert.equal(component(health, 'vswitchd').freshness, 'Unavailable');
  assert.match(component(health, 'vswitchd').value, /not running/);
});

test('no observations and denied service never become a healthy host', () => {
  for (const health of [
    derive('empty'),
    deriveHealth(null, control(), signals(), now),
  ]) {
    assert.equal(health.overall, 'Unknown');
    assert.equal(component(health, 'health-coverage').observedAt, 0);
    assert.equal(component(health, 'health-coverage').freshness, 'Unavailable');
  }
  const denied = { ...control(), scenario: 'permission-denied' };
  assert.deepEqual(derive('critical', denied).components, []);
  for (const scenario of [
    'permission-denied',
    'network-loss',
    'provider-unavailable',
    'error',
    'loading',
  ])
    assert.ok(healthReadBlock(scenario));
  assert.equal(healthReadBlock('normal'), null);
});

test('healthy health reads preserve every unresolved shared transaction and its checkpoint', () => {
  for (const status of [
    'countdown',
    'outcome-unknown',
    'rollback-conflict',
    'degraded',
    'needs-attention',
  ]) {
    const state = control();
    state.transaction.status = status;
    state.transaction.id = 'txn-health-check';
    state.transaction.deadline = now + 90_000;
    state.transaction.snapshot = {
      kind: 'bond',
      revision: 1,
      baseGeneration: generation,
      intent: { objectName: 'bond-sample' },
    };
    const before = structuredClone(state);
    const health = derive('healthy', state, signals(), now + 5000);
    assert.equal(
      component(health, 'change-safety').since,
      now + 5000,
      'new transaction observations do not inherit the older host snapshot time',
    );
    assert.equal(
      health.overall,
      status === 'countdown' ? 'Degraded' : 'Recovery Required',
    );
    assert.equal(component(health, 'change-safety').action.view, 'safe-apply');
    assert.match(
      component(health, 'change-safety').value,
      /checkpoint retained/,
    );
    assert.ok(transactionLocked(state.transaction.status));
    assert.deepEqual(state, before);
  }
});

test('member and LACP incidents preselect the correct bounded diagnostic scope', () => {
  for (const [scenario, scope] of [
    ['member-down', 'Port/bond-storage'],
    ['lacp-mismatch', 'Port/bond-uplink'],
  ]) {
    const row = component(
      derive('healthy', { ...control(), scenario }),
      'link',
    );
    assert.equal(row.status, 'Degraded');
    assert.deepEqual(row.action, {
      label:
        scenario === 'member-down'
          ? 'Prepare member diagnostic'
          : 'Prepare LACP diagnostic',
      diagnostic: 'diag.net.link-lacp',
      scope,
    });
  }
  assert.equal(
    component(
      derive('healthy', { ...control(), scenario: 'drift' }),
      'change-safety',
    ).action.view,
    'workspace',
  );
});

test('shared acceleration evidence overrides the health fixture without inventing freshness', () => {
  const observations = signals();
  observations.acceleration.snapshot = captureAcceleration(
    'unknown',
    now,
    generation,
  );
  assert.equal(
    component(derive('healthy', control(), observations), 'offload-provider')
      .status,
    'Unknown',
  );
  observations.acceleration.snapshot = captureAcceleration(
    'enabled',
    now,
    generation,
  );
  assert.equal(
    component(derive('healthy', control(), observations), 'offload-provider')
      .status,
    'Healthy',
  );
  for (const scenario of ['stale', 'network-loss', 'provider-unavailable']) {
    const row = component(
      derive('healthy', { ...control(), scenario }, observations),
      'offload-provider',
    );
    assert.equal(row.status, 'Unknown');
    assert.notEqual(row.freshness, 'Fresh');
  }
  observations.acceleration.failure = 'Read failed';
  assert.equal(
    component(derive('healthy', control(), observations), 'dpdk-provider')
      .freshness,
    'Unavailable',
  );
});

test('OpenFlow coverage keeps its own scope, TTL and missing generation provenance', () => {
  const observations = signals();
  observations.openFlow.snapshot = collectFlowSnapshot(
    { ...defaultFlowQuery, bridge: 'br-storage' },
    'fresh',
    now,
    450,
  ).snapshot;
  const row = component(
    derive('healthy', control(), observations),
    'flow-collection',
  );
  assert.equal(row.related, 'Bridge/br-storage');
  assert.equal(row.observedAt, now);
  assert.equal(row.generation, null);
  assert.equal(row.status, 'Healthy');
  assert.equal(
    component(
      derive('healthy', control(), observations, now + 30_000),
      'flow-collection',
    ).status,
    'Unknown',
  );
  observations.openFlow.failure = 'query-timeout';
  assert.equal(
    component(derive('healthy', control(), observations), 'flow-collection')
      .freshness,
    'Unavailable',
  );
});

test('Job failures and missing results remain explicit, while a preview is not an executed Job', () => {
  const observations = signals();
  for (const [state, expected] of [
    ['failed', 'Degraded'],
    ['partial', 'Degraded'],
    ['expired', 'Unknown'],
    ['unavailable', 'Unknown'],
    ['no-data', 'Unknown'],
    ['running', 'Healthy'],
  ]) {
    observations.diagnostic.state = state;
    assert.equal(
      component(derive('healthy', control(), observations), 'diagnostic-job')
        .status,
      expected,
    );
  }
  observations.diagnostic.state = 'failed';
  observations.diagnostic.reviewOnly = true;
  assert.equal(
    component(derive('healthy', control(), observations), 'diagnostic-job')
      .status,
    'Healthy',
  );
});

test('incident age survives refresh; recovery emits one Event without mutating Audit history', () => {
  const first = derive('degraded').components;
  const refresh = deriveHealth(
    captureHealth('degraded', now + 5000, generation),
    control(),
    signals(),
    now + 5000,
  ).components;
  const stable = preserveHealthSince(first, refresh, now + 5000);
  assert.equal(stable.find((row) => row.id === 'telemetry').since, now);
  assert.deepEqual(healthEvents(first, refresh, now + 5000), []);
  const recovering = preserveHealthSince(
    stable,
    derive('recovering').components,
    now + 6000,
  );
  const events = healthEvents(stable, recovering, now + 6000);
  assert.equal(events.length, 1);
  assert.equal(events[0].kind, 'Event');
  assert.match(events[0].text, /Degraded → Recovering/);
  assert.equal(
    recovering.find((row) => row.id === 'telemetry').since,
    now + 6000,
  );
  assert.deepEqual(healthEvents([], recovering, now), []);
  assert.deepEqual(healthEvents(recovering, recovering, now + 9000), []);
  const changedScope = recovering.map((row) => ({
    ...row,
    related: 'Another resource',
    status: 'Healthy',
  }));
  assert.deepEqual(
    healthEvents(recovering, changedScope, now + 9000),
    [],
    'a different object does not resolve this incident',
  );
  assert.deepEqual(
    healthEvents(
      recovering,
      recovering.map((row) => ({
        ...row,
        generation: generation + 1,
        status: 'Healthy',
      })),
      now + 9000,
    ),
    [],
    'a new instance generation starts a separate baseline',
  );
  const state = control();
  state.evidence.push({
    at: now,
    kind: 'Audit',
    text: 'Existing authorized operation',
    object: 'Workspace',
    correlation: 'corr-original',
  });
  const before = structuredClone(state);
  derive('healthy', state);
  assert.deepEqual(state, before);
});
