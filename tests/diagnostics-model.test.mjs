import test from 'node:test';
import assert from 'node:assert/strict';
import {
  diagnostics,
  diagnosticCancelBlock,
  diagnosticExport,
  diagnosticJobTone,
  diagnosticResult,
} from '../lib/diagnostics-model.ts';
import {
  captureDiagnosticRequest,
  diagnosticRunBlock,
  runnableDiagnostics,
} from '../lib/p1-control.ts';

const network = {
  id: 'diag.net.link-lacp',
  scope: 'Port/bond-storage',
  sampleSeconds: 5,
  detail: 'bounded',
};
const collection = {
  id: 'diag.openflow.collection',
  scope: 'Bridge/br-fabric',
  sampleSeconds: 10,
  detail: 'structured',
};

void test('catalog capabilities agree with runnable allowlist and budgets fit their timeout', () => {
  assert.deepEqual(
    diagnostics
      .filter((item) => item.availability === 'Available')
      .map((item) => item.id),
    [...runnableDiagnostics],
  );
  for (const definition of diagnostics)
    assert.ok(
      definition.sampleBudgets.every((budget) => budget <= definition.timeout),
    );
});

void test('capturing input and starting a Job reject incompatible target, budget and output without normalization', () => {
  for (const request of [
    { ...collection, scope: 'Port/bond-storage' },
    { ...collection, sampleSeconds: 15 },
    { ...collection, detail: 'bounded' },
    { ...network, id: 'diag.ovs.datapath-trace' },
  ]) {
    assert.ok(
      diagnosticRunBlock(
        request.id,
        request.scope,
        'valid',
        false,
        1440,
        request,
      ),
    );
    assert.throws(() =>
      captureDiagnosticRequest(request.id, request.scope, request),
    );
  }
  assert.deepEqual(
    captureDiagnosticRequest(collection.id, collection.scope, collection),
    collection,
  );
  assert.equal(
    diagnosticRunBlock(
      collection.id,
      collection.scope,
      'valid',
      false,
      820,
      collection,
    ),
    null,
  );
});

void test('unavailable and missing evidence states never become successful health results or exports', () => {
  for (const state of [
    'not-started',
    'queued',
    'running',
    'cancel-requested',
    'cancelled',
    'failed',
    'command-failed',
    'expired',
    'unavailable',
    'provider-unavailable',
    'no-data',
  ]) {
    const result = diagnosticResult(network, state);
    assert.equal(result.outputAvailable, false, state);
    assert.equal(result.raw, null, state);
    assert.equal(diagnosticExport(network, state), null, state);
    assert.notEqual(diagnosticJobTone(state), 'success', state);
  }
  assert.match(diagnosticResult(network, 'no-data').finding, /health unknown/);
  assert.match(
    diagnosticResult(network, 'unavailable').body,
    /does not prove that execution failed/,
  );
  assert.equal(diagnosticJobTone('no-finding'), 'success');
  assert.equal(diagnosticResult(network, 'running').evidenceAvailable, false);
  assert.match(
    diagnosticResult(network, 'no-finding').body,
    /unsampled objects/,
  );
});

void test('storage evidence retains a known carrier fault while missing counters remain unknown', () => {
  const partial = diagnosticResult(network, 'partial');
  assert.match(partial.finding, /carrier down.*counters unknown/);
  assert.match(partial.raw, /counter_sample=unknown coverage=partial/);
  assert.doesNotMatch(partial.raw, /partner.mismatch|lacp=active/);
  const complete = diagnosticResult(network, 'complete');
  assert.equal(complete.tone, 'warning');
  assert.match(complete.raw, /lacp=off bond_mode=active-backup/);
  const other = diagnosticResult(
    { ...network, scope: 'Port/bond-uplink' },
    'complete',
  );
  assert.doesNotMatch(other.finding + other.raw, /enp130s0f1|carrier down/);
});

void test('output limits, actual excerpt counts and truncation are preserved in export', () => {
  const trace = {
    ...network,
    id: 'diag.ovs.datapath-trace',
    scope: 'Bridge/br-fabric',
  };
  const result = diagnosticResult(trace, 'truncated');
  const output = diagnosticExport(trace, 'truncated');
  assert.equal(result.limits.textLines, 300);
  assert.equal(output.limits.bytes, 65536);
  assert.equal(output.truncated, true);
  assert.equal(output.textExcerptLines, output.raw.split('\n').length);
  assert.ok(output.textExcerptLines < output.limits.textLines);
  assert.equal(diagnosticResult(network, 'complete').limits.textLines, 200);
});

void test('structured-only results never expose or export text, including truncated review cases', () => {
  for (const request of [collection, { ...network, detail: 'structured' }])
    for (const state of [
      'complete',
      'truncated',
      'partial',
      'no-finding',
      'evidence-unavailable',
    ]) {
      const result = diagnosticResult(request, state);
      const output = diagnosticExport(request, state);
      assert.equal(result.raw, null);
      assert.equal(output.textExcerptLines, 0);
      assert.equal(Object.hasOwn(output, 'raw'), false);
    }
  assert.equal(diagnosticResult(collection, 'complete').limits.textLines, 0);
});

void test('Event link failure preserves the captured result and clearly labels preview exports', () => {
  const result = diagnosticResult(network, 'evidence-unavailable');
  assert.equal(result.evidenceAvailable, false);
  assert.equal(result.outputAvailable, true);
  const output = diagnosticExport(network, 'evidence-unavailable', true);
  assert.equal(output.reviewOnly, true);
  assert.equal(output.prototype, true);
  output.request.scope = 'Port/changed-export';
  assert.equal(network.scope, 'Port/bond-storage');
});

void test('service uncertainty blocks new work and cancellation while degraded collection remains possible', () => {
  assert.ok(
    diagnosticRunBlock(
      network.id,
      network.scope,
      'valid',
      false,
      1440,
      network,
      'empty',
    ),
  );
  for (const scenario of [
    'permission-denied',
    'provider-unavailable',
    'network-loss',
    'error',
    'loading',
  ]) {
    assert.ok(
      diagnosticRunBlock(
        network.id,
        network.scope,
        'valid',
        false,
        1440,
        network,
        scenario,
      ),
    );
    assert.ok(diagnosticCancelBlock(network.id, 'running', scenario, 1440));
  }
  assert.equal(
    diagnosticRunBlock(
      network.id,
      network.scope,
      'valid',
      false,
      820,
      network,
      'provider-degraded',
    ),
    null,
  );
  assert.equal(
    diagnosticCancelBlock(network.id, 'running', 'normal', 820),
    null,
  );
  assert.ok(diagnosticCancelBlock(collection.id, 'running', 'normal', 1440));
  assert.ok(
    diagnosticCancelBlock(network.id, 'cancel-requested', 'normal', 1440),
  );
  assert.ok(diagnosticCancelBlock(network.id, 'complete', 'normal', 1440));
  assert.ok(diagnosticCancelBlock(network.id, 'running', 'normal', 390));
});
