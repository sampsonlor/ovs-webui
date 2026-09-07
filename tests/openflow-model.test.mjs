import test from 'node:test';
import assert from 'node:assert/strict';
import {
  captureFlowQuery,
  boundFlowSnapshot,
  collectFlowSnapshot,
  defaultFlowQuery,
  flowAdmission,
  flowFreshness,
  flowOutputBytes,
  flowPage,
  flowQueryErrors,
  openFlowLimits,
  serializeFlowSnapshot,
} from '../lib/openflow-model.ts';

const now = Date.parse('2026-09-07T09:00:00Z');
const capture = (
  query = defaultFlowQuery,
  reviewCase = 'fresh',
  time = now,
  elapsed = 450,
  degraded = false,
) => collectFlowSnapshot(query, reviewCase, time, elapsed, degraded);

test('a captured request and its rows are independent of later draft edits', () => {
  const draft = { ...defaultFlowQuery };
  const request = captureFlowQuery(draft);
  const snapshot = capture(draft).snapshot;
  draft.bridge = 'br-storage';
  draft.table = '90';
  assert.equal(request.bridge, 'br-fabric');
  assert.deepEqual(snapshot.query, defaultFlowQuery);
  assert.equal(snapshot.rows.length, 8);
});

test('Bridge fixtures preserve protocol, ownership and typed related objects', () => {
  const fabric = capture().snapshot;
  const storage = capture({
    ...defaultFlowQuery,
    bridge: 'br-storage',
  }).snapshot;
  assert.equal(storage.source.protocol, 'OpenFlow 1.3');
  assert.equal(fabric.source.authority, 'External controller');
  assert.ok(fabric.rows.every((row) => !row.relatedObject.includes('storage')));
  assert.ok(
    storage.rows.every(
      (row) =>
        row.relatedObject.endsWith('br-storage') ||
        row.relatedObject === 'Port/bond-storage',
    ),
  );
  assert.equal(
    fabric.rows.find((row) => row.action === 'drop').relatedObject,
    'Bridge/br-fabric',
  );
  assert.deepEqual(capture({ ...defaultFlowQuery, bridge: 'br-offload' }), {
    snapshot: null,
    failure: 'provider-unavailable',
  });
});

test('invalid priority, catalog scope and oversized text fail before collection', () => {
  for (const priority of ['-1', '65536', '1e2', '1.5', 'abc']) {
    const query = { ...defaultFlowQuery, priority };
    assert.ok(flowQueryErrors(query).priority);
    assert.throws(() => capture(query));
  }
  assert.deepEqual(
    flowQueryErrors({ ...defaultFlowQuery, priority: '65535' }),
    {},
  );
  assert.ok(flowQueryErrors({ ...defaultFlowQuery, inPort: '7' }).inPort);
  assert.ok(
    flowQueryErrors({ ...defaultFlowQuery, bridge: 'br-missing' }).bridge,
  );
  assert.ok(
    flowQueryErrors({ ...defaultFlowQuery, search: 'x'.repeat(129) }).search,
  );
});

test('viewer filters combine on the captured population without shell or regex evaluation', () => {
  const query = {
    ...defaultFlowQuery,
    table: '10',
    priority: '150',
    match: '4C:21',
    action: 'OUTPUT:12',
    search: '0x0000a102',
  };
  assert.equal(capture(query).snapshot.rows.length, 1);
  assert.equal(
    capture({ ...defaultFlowQuery, search: '.*' }).snapshot.rows.length,
    0,
  );
  assert.equal(
    capture({ ...defaultFlowQuery, priority: '65535' }).snapshot.rows.length,
    0,
  );
});

test('large collections obey both caps and export raw and parsed values consistently', () => {
  const snapshot = capture(defaultFlowQuery, 'truncated').snapshot;
  assert.equal(snapshot.matchedRows, 768);
  assert.ok(
    snapshot.rows.length > 0 && snapshot.rows.length <= openFlowLimits.rows,
  );
  assert.ok(snapshot.truncation.includes('row-limit'));
  assert.ok(flowOutputBytes(snapshot, now) <= openFlowLimits.bytes);
  assert.equal(
    new Set(
      snapshot.rows.map((row) => `${row.table}:${row.priority}:${row.match}`),
    ).size,
    snapshot.rows.length,
  );
  for (const row of snapshot.rows) {
    for (const value of [
      `cookie=${row.cookie}`,
      `n_packets=${row.packets}`,
      `n_bytes=${row.bytes}`,
      `duration=${row.duration}s`,
      `actions=${row.action}`,
    ])
      assert.ok(row.raw.includes(value));
  }
  const exportText = serializeFlowSnapshot(snapshot, now + 60000, true);
  assert.ok(Buffer.byteLength(exportText) <= openFlowLimits.bytes);
  const exported = JSON.parse(exportText);
  assert.equal(exported.freshness, 'Stale');
  assert.equal(exported.coverage, 'Partial');
  assert.deepEqual(exported.snapshot.rows, snapshot.rows);
});

test('narrowing a large population can produce a complete bounded result', () => {
  const snapshot = capture(
    { ...defaultFlowQuery, table: '10' },
    'truncated',
  ).snapshot;
  assert.equal(snapshot.rows.length, 192);
  assert.deepEqual(snapshot.truncation, []);
  assert.equal(
    JSON.parse(serializeFlowSnapshot(snapshot, now)).coverage,
    'Complete for captured query',
  );
});

test('large opaque source fields enforce the byte cap without rewriting retained raw rows', () => {
  const input = capture(defaultFlowQuery, 'truncated').snapshot;
  input.rows = input.rows.map((row) => ({
    ...row,
    raw: row.raw + ',provider_extension=' + 'opaque'.repeat(1000),
  }));
  const bounded = boundFlowSnapshot(input, now);
  assert.ok(bounded.truncation.includes('byte-limit'));
  assert.ok(bounded.rows.length > 0 && bounded.rows.length < input.rows.length);
  assert.equal(bounded.rows[0].raw, input.rows[0].raw);
  assert.ok(flowOutputBytes(bounded, now + 60000) <= openFlowLimits.bytes);
  assert.equal(
    JSON.parse(serializeFlowSnapshot(bounded, now, false, true))
      .currentCollectionUnavailable,
    true,
  );
});

test('permission, unavailable provider and timeout never supply fallback rows', () => {
  for (const failure of [
    'permission-denied',
    'provider-unavailable',
    'query-timeout',
  ])
    assert.deepEqual(capture(defaultFlowQuery, failure), {
      snapshot: null,
      failure,
    });
  assert.deepEqual(capture(defaultFlowQuery, 'fresh', now, 5000), {
    snapshot: null,
    failure: 'query-timeout',
  });
});

test('empty success has no selected detail, including when the previous page was late', () => {
  const snapshot = capture(defaultFlowQuery, 'empty').snapshot;
  assert.equal(snapshot.rows.length, 0);
  assert.deepEqual(flowPage(snapshot.rows, 99, 'br-fabric-flow-0'), {
    page: 1,
    pageCount: 1,
    visible: [],
    selected: null,
  });
});

test('selection follows visible pages and clamps after a smaller capture', () => {
  const rows = capture().snapshot.rows;
  const second = flowPage(rows, 2, rows[0].id);
  assert.equal(second.selected.id, rows[4].id);
  const small = capture({ ...defaultFlowQuery, bridge: 'br-storage' }).snapshot
    .rows;
  assert.equal(flowPage(small, 99, rows[4].id).page, 1);
  assert.equal(flowPage(small, 99, rows[4].id).selected.id, small[0].id);
});

test('freshness ages independently of collection coverage and degraded evidence remains partial', () => {
  const fresh = capture().snapshot;
  assert.equal(flowFreshness(fresh, now + 29999), 'Fresh');
  assert.equal(flowFreshness(fresh, now + 30000), 'Stale');
  assert.equal(
    flowFreshness(capture(defaultFlowQuery, 'stale').snapshot, now),
    'Stale',
  );
  const degraded = capture(defaultFlowQuery, 'fresh', now, 450, true).snapshot;
  assert.equal(
    JSON.parse(serializeFlowSnapshot(degraded, now)).coverage,
    'Partial',
  );
  assert.equal(
    JSON.parse(serializeFlowSnapshot(fresh, now, true)).coverage,
    'Partial',
  );
});

test('all collection entry points share mobile, busy and service gates', () => {
  assert.match(
    flowAdmission(defaultFlowQuery, 'normal', 390, false),
    /tablet or desktop/,
  );
  assert.match(
    flowAdmission(defaultFlowQuery, 'normal', 1440, true),
    /active collection/,
  );
  for (const scenario of [
    'loading',
    'empty',
    'error',
    'permission-denied',
    'provider-unavailable',
    'network-loss',
  ])
    assert.ok(flowAdmission(defaultFlowQuery, scenario, 1440, false));
  for (const scenario of [
    'normal',
    'degraded',
    'provider-degraded',
    'outcome-unknown',
    'drift',
    'conflict',
  ])
    assert.equal(flowAdmission(defaultFlowQuery, scenario, 820, false), null);
});
