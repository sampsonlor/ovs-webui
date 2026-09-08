import test from 'node:test';
import assert from 'node:assert/strict';
import {
  initialControlState,
  transition,
  candidateObject,
  candidateBridge,
  applyBlock,
} from '../lib/change-control.ts';
import {
  representativeBondIntent,
  diagnosticRunBlock,
  captureDiagnosticRequest,
} from '../lib/p1-control.ts';
import { ports } from '../lib/ovs-model.ts';

void test('diagnostic request captures its original scope and bounded options independently of later edits', () => {
  const draft = { sampleSeconds: 5, detail: 'bounded' };
  const request = captureDiagnosticRequest(
    'diag.net.link-lacp',
    'Port/bond-uplink',
    draft,
  );
  draft.sampleSeconds = 15;
  draft.detail = 'structured';
  assert.deepEqual(request, {
    id: 'diag.net.link-lacp',
    scope: 'Port/bond-uplink',
    sampleSeconds: 5,
    detail: 'bounded',
  });
});

void test('diagnostic request rejects unbounded durations, unknown output formats and unavailable templates', () => {
  for (const parameters of [
    { sampleSeconds: 0, detail: 'structured' },
    { sampleSeconds: 20, detail: 'bounded' },
    { sampleSeconds: 10, detail: 'raw-shell' },
  ])
    assert.throws(() =>
      captureDiagnosticRequest(
        'diag.net.link-lacp',
        'Port/bond-uplink',
        parameters,
      ),
    );
  assert.throws(() =>
    captureDiagnosticRequest(
      'diag.host.interface-counters',
      'Port/bond-uplink',
      { sampleSeconds: 5, detail: 'structured' },
    ),
  );
});

const bond = representativeBondIntent({
  name: 'bond-review',
  bridge: 'br-fabric',
  mode: 'balance-tcp',
  lacp: 'active',
});
const bridge = {
  kind: 'bridge',
  objectType: 'Bridge',
  objectName: 'br-review',
  bridgeName: 'br-review',
  title: 'Create Bridge',
  summary: 'Create native Bridge',
  current: 'object: absent',
  candidate: 'name: br-review\nrstp_enable: true',
  risk: 'Medium',
  capability: 'bridge.manage',
  evidenceObject: 'Bridge/br-review',
};
const vlan = {
  type: 'stage',
  port: ports[1],
  mine: { mode: 'trunk', tag: null, trunks: '120,240' },
  desktop: true,
  now: 1000,
};
const act = (state, type, extra = {}) =>
  transition(state, { type, now: 1000, ...extra });
const stage = (intent = bond) =>
  act(structuredClone(initialControlState), 'stage-topology', {
    intent,
    desktop: true,
  });
const ready = (intent = bond) =>
  act(act(stage(intent), 'validate'), 'note', {
    note: 'Review topology integration',
  });
const active = (intent = bond) =>
  act(ready(intent), 'start', { desktop: true });

void test('Bridge and Bond keep native identity through Candidate, transaction and evidence', () => {
  for (const intent of [bridge, bond]) {
    const state = active(intent);
    assert.equal(state.error, null);
    assert.equal(
      candidateObject(state.transaction.snapshot),
      intent.evidenceObject,
    );
    assert.equal(
      candidateBridge(state.transaction.snapshot),
      intent.bridgeName,
    );
    const result = act(state, 'confirm');
    assert.equal(result.transaction.status, 'confirmed');
    assert.equal(result.candidate, null);
    assert.deepEqual(result.live, {});
    assert.equal(result.evidence.at(-1).object, intent.evidenceObject);
  }
});

void test('P0 and P1 intents cannot replace one another or stage on mobile/Observe objects', () => {
  assert.ok(transition(stage(), vlan).error);
  assert.ok(
    act(transition(initialControlState, vlan), 'stage-topology', {
      intent: bond,
      desktop: true,
    }).error,
  );
  assert.ok(
    act(initialControlState, 'stage-topology', {
      intent: bridge,
      desktop: false,
    }).error,
  );
  for (const intent of [
    { ...bond, bridgeName: 'br-offload' },
    { ...bond, objectType: 'Bridge' },
    { ...bridge, current: bridge.candidate },
  ])
    assert.ok(
      act(initialControlState, 'stage-topology', { intent, desktop: true })
        .error,
    );
});

void test('unknown topology transaction stays locked across diagnostics and VLAN attempts', () => {
  const unknown = act(active(), 'scenario', { scenario: 'outcome-unknown' });
  const withDiagnostic = act(unknown, 'record-evidence', {
    kind: 'Event',
    text: 'Diagnostic complete',
    object: 'Port/bond-storage',
    correlation: 'corr-DIAG-91C4',
  });
  assert.deepEqual(withDiagnostic.transaction, unknown.transaction);
  assert.deepEqual(withDiagnostic.candidate, unknown.candidate);
  assert.equal(withDiagnostic.evidence.at(-1).correlation, 'corr-DIAG-91C4');
  for (const attempt of [
    transition(withDiagnostic, vlan),
    act(withDiagnostic, 'start', { desktop: true }),
    act(withDiagnostic, 'confirm'),
    act(withDiagnostic, 'discard'),
  ])
    assert.ok(attempt.error);
  const normal = act(withDiagnostic, 'scenario', { scenario: 'normal' });
  assert.equal(normal.transaction.status, 'outcome-unknown');
});

void test('topology rebase invalidates validation and conflicting native values cannot be forced', () => {
  const stale = act(ready(), 'scenario', { scenario: 'stale' });
  const rebased = act(stale, 'rebase', { choice: 'non-overlapping' });
  assert.equal(rebased.validatedRevision, null);
  assert.deepEqual(rebased.candidate.intent, bond);
  assert.match(applyBlock(rebased), /Validate/);
  const conflict = act(ready(bridge), 'scenario', { scenario: 'conflict' });
  assert.ok(act(conflict, 'rebase', { choice: 'mine' }).error);
  assert.equal(act(conflict, 'rebase', { choice: 'current' }).candidate, null);
});

void test('topology confirmation cannot succeed after disconnected expiry or rollback conflict', () => {
  const disconnected = act(active(), 'scenario', { scenario: 'network-loss' });
  const expired = act(disconnected, 'tick', { now: 100_000 });
  assert.equal(expired.transaction.status, 'outcome-unknown');
  assert.ok(act(expired, 'confirm').error);
  const conflict = act(active(bridge), 'scenario', {
    scenario: 'rollback-conflict',
  });
  assert.equal(
    act(conflict, 'rollback').transaction.status,
    'rollback-conflict',
  );
});

void test('P1 programmatic entry points enforce managed Bridge, LACP, bounded scope and device gates', () => {
  for (const override of [
    { bridge: 'br-offload' },
    { name: 'bad/name' },
    { mode: 'arbitrary' },
    { lacp: 'off' },
  ])
    assert.throws(() =>
      representativeBondIntent({
        name: 'bond-new',
        bridge: 'br-fabric',
        mode: 'balance-tcp',
        lacp: 'active',
        ...override,
      }),
    );
  assert.equal(
    diagnosticRunBlock(
      'diag.net.link-lacp',
      'Port/bond-storage',
      'valid',
      false,
      1024,
    ),
    null,
  );
  assert.ok(
    diagnosticRunBlock(
      'diag.net.link-lacp',
      'Port/bond-storage',
      'valid',
      false,
      390,
    ),
  );
  assert.ok(
    diagnosticRunBlock(
      'diag.host.interface-counters',
      'Port/bond-storage',
      'valid',
      false,
      1024,
    ),
  );
  assert.ok(
    diagnosticRunBlock('diag.net.link-lacp', '*', 'valid', false, 1024),
  );
  assert.ok(
    diagnosticRunBlock(
      'diag.net.link-lacp',
      'Port/bond-storage',
      'scope-too-broad',
      false,
      1024,
    ),
  );
  assert.ok(
    diagnosticRunBlock(
      'diag.net.link-lacp',
      'Port/bond-storage',
      'valid',
      true,
      1024,
    ),
  );
});
