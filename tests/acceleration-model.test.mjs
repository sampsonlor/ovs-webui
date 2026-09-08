import test from 'node:test';
import assert from 'node:assert/strict';
import {
  accelerationAdmission,
  accelerationServiceBlock,
  accelerationTtlMs,
  assessAcceleration,
  captureAcceleration,
  filterAcceleration,
  observationFreshness,
  observationText,
  projectAccelerationRecord,
} from '../lib/acceleration-model.ts';

const now = Date.parse('2026-09-07T10:00:00Z');
const generation = 1842;
const capture = (state = 'enabled') =>
  captureAcceleration(state, now, generation);
const assess = (record, time = now, gen = generation, scenario = 'normal') =>
  assessAcceleration(record, time, gen, scenario);
const fact = (record, key) => record.facts.find((item) => item.key === key);

for (const [reviewCase, expected] of [
  ['enabled', 'Enabled'],
  ['available', 'Available'],
  ['missing', 'Missing prerequisites'],
  ['unsupported', 'Unsupported'],
  ['unknown', 'Unknown'],
]) {
  test(`both capability families derive ${expected} from authoritative evidence`, () => {
    for (const record of capture(reviewCase).records)
      assert.equal(assess(record).state, expected);
  });
}

test('normal retains independent DPDK and offload coverage, with vhost unknown', () => {
  const [dpdk, offload] = capture('normal').records;
  assert.equal(assess(dpdk).state, 'Enabled');
  assert.equal(assess(offload).state, 'Unknown');
  assert.equal(fact(dpdk, 'vhost').value, null);
  assert.equal(fact(dpdk, 'datapath').value, 'netdev');
  fact(dpdk, 'supported').value = null;
  assert.equal(
    assess(dpdk).state,
    'Unknown',
    'netdev must not imply DPDK support',
  );
});

test('configured and operational contradictions never imply Enabled or Available', () => {
  for (const record of capture('mismatch').records) {
    assert.equal(assess(record).state, 'Unknown');
    fact(record, 'configured').value = false;
    fact(record, 'operational').value = true;
    assert.equal(assess(record).state, 'Unknown');
    fact(record, 'operational').value = null;
    assert.equal(assess(record).state, 'Unknown');
  }
});

test('a missing prerequisite is an explicit negative, not missing evidence', () => {
  const record = capture('missing').records[0];
  assert.equal(assess(record).state, 'Missing prerequisites');
  fact(record, 'memory').value = null;
  assert.equal(assess(record).state, 'Unknown');
  fact(record, 'configured').value = true;
  fact(record, 'operational').value = true;
  assert.equal(
    assess(record).state,
    'Unknown',
    'runtime cannot override unknown prerequisite evidence',
  );
});

test('capability decisions expire exactly at the freshness boundary', () => {
  for (const record of capture().records) {
    assert.equal(assess(record, now + accelerationTtlMs - 1).state, 'Enabled');
    assert.equal(assess(record, now + accelerationTtlMs).state, 'Unknown');
    assert.equal(
      assess(record, now - 1).state,
      'Unknown',
      'future timestamps cannot establish readiness',
    );
  }
  assert.ok(
    capture('stale').records.every(
      (record) => assess(record).state === 'Unknown',
    ),
  );
});

test('instance and generation mismatches invalidate individual required facts', () => {
  const record = capture().records[0];
  fact(record, 'operational').generation--;
  assert.equal(assess(record).state, 'Unknown');
  fact(record, 'operational').generation = generation;
  fact(record, 'memory').instance = 'another-ovs-instance';
  assert.equal(assess(record).state, 'Unknown');
  assert.equal(
    observationFreshness(fact(record, 'memory'), now, generation),
    'Generation mismatch',
  );
  assert.ok(
    capture('generation-mismatch').records.every(
      (item) => assess(item).state === 'Unknown',
    ),
  );
});

test('provider outage cannot be mistaken for Unsupported or current Enabled', () => {
  for (const reviewCase of ['enabled', 'unsupported'])
    for (const record of capture(reviewCase).records) {
      for (const scenario of [
        'provider-unavailable',
        'network-loss',
        'error',
        'permission-denied',
        'loading',
        'stale',
      ])
        assert.equal(
          assess(record, now, generation, scenario).state,
          'Unknown',
        );
    }
  for (const record of capture('unavailable').records) {
    assert.equal(assess(record).state, 'Unknown');
    assert.ok(
      record.facts
        .filter((item) => item.authority !== 'Configuration')
        .every((item) => item.value === null),
    );
  }
});

test('partial telemetry can retain Enabled while counters remain unknown', () => {
  const capturedDuringDegradation = captureAcceleration(
    'enabled',
    now,
    generation,
    'provider-degraded',
  );
  for (const observed of capturedDuringDegradation.records) {
    const afterRecovery = projectAccelerationRecord(observed, 'normal');
    assert.equal(afterRecovery.provider, 'Degraded');
    assert.ok(
      afterRecovery.facts
        .filter((item) =>
          ['pmd-cycles', 'rx-drops', 'hw-flows', 'sw-flows'].includes(item.key),
        )
        .every((item) => item.value === null),
      'restoring the service cannot fill telemetry absent from the captured observation',
    );
  }
  for (const record of capture().records) {
    const projected = projectAccelerationRecord(record, 'provider-degraded');
    assert.equal(projected.provider, 'Degraded');
    assert.equal(assess(projected).state, 'Enabled');
    const counters = projected.facts.filter((item) =>
      ['pmd-cycles', 'rx-drops', 'hw-flows', 'sw-flows'].includes(item.key),
    );
    assert.ok(counters.length);
    assert.ok(counters.every((item) => item.value === null));
    assert.ok(
      record.facts
        .filter((item) => counters.some((counter) => counter.key === item.key))
        .every((item) => item.value !== null),
      'projection must not mutate the captured snapshot',
    );
  }
});

test('fallback evidence preserves actual zero hardware entries and separate software counts', () => {
  const offload = capture('fallback').records[1];
  assert.equal(assess(offload).state, 'Enabled');
  assert.equal(fact(offload, 'hw-flows').value, 0);
  assert.equal(fact(offload, 'sw-flows').value, 12);
  assert.match(fact(offload, 'fallback-reason').value, /unsupported action/);
  assert.equal(observationText(0), '0');
  assert.equal(observationText(null), 'Unknown');
  assert.equal(observationText(false), 'No');
});

test('representor role does not fabricate native type, a physical connector or PF/VF identity', () => {
  const offload = capture().records[1];
  assert.equal(fact(offload, 'native-type').value, null);
  assert.equal(fact(offload, 'pf-vf').value, null);
  assert.match(fact(offload, 'representor').source, /topology provider/);
  assert.match(offload.related, /provider-declared representor/);
  for (const state of ['unknown', 'unavailable', 'missing']) {
    const uncertain = capture(state).records[1];
    assert.match(uncertain.related, /hardware role unknown/);
    assert.doesNotMatch(
      fact(uncertain, 'representor').note,
      /declares Interface/,
    );
  }
});

test('empty reads and mismatched filters do not select another hidden capability', () => {
  assert.deepEqual(capture('empty').records, []);
  const records = capture('normal').records;
  assert.deepEqual(
    filterAcceleration(
      records,
      'hardware',
      'Enabled',
      now,
      generation,
      'normal',
    ),
    [],
  );
  assert.deepEqual(
    filterAcceleration(
      records,
      '  HARDWARE ',
      'Unknown',
      now,
      generation,
      'normal',
    ).map((item) => item.id),
    ['offload'],
  );
  assert.equal(
    filterAcceleration(
      records,
      '',
      'Unknown',
      now + accelerationTtlMs,
      generation,
      'normal',
    ).length,
    2,
  );
});

test('observation admission shares service, busy and responsive gates without transaction writes', () => {
  for (const scenario of [
    'permission-denied',
    'loading',
    'error',
    'network-loss',
    'provider-unavailable',
  ])
    assert.ok(accelerationAdmission(scenario, 1440, false));
  assert.ok(accelerationAdmission('normal', 767, false));
  assert.ok(accelerationAdmission('normal', 1440, true));
  assert.equal(accelerationAdmission('normal', 768, false), null);
  for (const scenario of [
    'outcome-unknown',
    'validation-blocked',
    'drift',
    'provider-degraded',
  ]) {
    assert.equal(accelerationServiceBlock(scenario), null);
    assert.equal(accelerationAdmission(scenario, 1440, false), null);
  }
});
