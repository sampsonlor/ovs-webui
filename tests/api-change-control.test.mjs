import test from 'node:test';
import assert from 'node:assert/strict';
import {
  submitSafeApplyOnce,
  recoverSafeApply,
  presentTransaction,
  acceptTransactionSnapshot,
} from '../lib/api/change-control.ts';
import {
  command,
  provisional,
  confirmed,
  unknown,
  rollbackConflict,
  requestRecord,
  problem,
} from '../contracts/examples/core-fixtures.mjs';

const context = {
  connected: true,
  fresh: true,
  receivedAtMonotonicMs: 1000,
  nowMonotonicMs: 1000,
};
const handle = { requestId: command.requestId, nodeId: provisional.nodeId };
function gateway(overrides = {}) {
  return {
    async startSafeApply() {
      return { kind: 'accepted', transaction: structuredClone(provisional) };
    },
    async readRequest() {
      return structuredClone(requestRecord);
    },
    async readTransaction() {
      return structuredClone(provisional);
    },
    ...overrides,
  };
}

test('lost acceptance reply is recovered from the original request without a second write', async () => {
  let writes = 0;
  let accepted = false;
  const calls = [];
  const api = gateway({
    async startSafeApply(input) {
      writes++;
      accepted = true;
      assert.equal(input.requestId, handle.requestId);
      throw new Error('socket lost after durable acceptance');
    },
    async readRequest(id) {
      calls.push(['request', id]);
      return accepted ? requestRecord : null;
    },
    async readTransaction(id) {
      calls.push(['transaction', id]);
      return provisional;
    },
  });
  const submission = await submitSafeApplyOnce(api, handle.nodeId, command);
  assert.deepEqual(submission, { kind: 'unknown', handle });
  const recovered = await recoverSafeApply(api, submission.handle);
  assert.equal(recovered.kind, 'accepted');
  assert.equal(recovered.transaction.id, provisional.id);
  assert.equal(writes, 1);
  assert.deepEqual(calls, [
    ['request', command.requestId],
    ['transaction', provisional.id],
  ]);
});

test('missing, still-recording, disconnected and wrong-scope ledger reads stay unknown', async () => {
  for (const readRequest of [
    async () => null,
    async () => ({ ...requestRecord, state: 'recorded', transactionId: null }),
    async () => {
      throw new Error('unavailable');
    },
    async () => ({ ...requestRecord, nodeId: 'other-node' }),
    async () => ({ ...requestRecord, operation: 'decision' }),
    async () => ({ ...requestRecord, requestId: 'another-request' }),
  ]) {
    const api = gateway({
      readRequest,
      async startSafeApply() {
        assert.fail('Recovery must not write');
      },
    });
    assert.deepEqual(await recoverSafeApply(api, handle), {
      kind: 'unknown',
      handle,
    });
  }
});

test('only an authoritative rejection for this request is treated as not started', async () => {
  for (const reply of [
    problem,
    { ...problem, commandEffect: 'unknown' },
    { ...problem, requestId: 'other-request' },
  ]) {
    const result = await submitSafeApplyOnce(
      gateway({
        async startSafeApply() {
          return { kind: 'problem', problem: reply };
        },
      }),
      handle.nodeId,
      command,
    );
    assert.equal(result.kind, reply === problem ? 'rejected' : 'unknown');
  }
  const recovered = await recoverSafeApply(
    gateway({
      async readRequest() {
        return {
          ...requestRecord,
          state: 'rejected',
          transactionId: null,
          problem,
        };
      },
    }),
    handle,
  );
  assert.equal(recovered.kind, 'rejected');
});

test('misrouted acceptance and recovery responses cannot attach a different transaction', async () => {
  for (const tx of [
    { ...provisional, requestId: 'other-request' },
    { ...provisional, nodeId: 'other-node' },
    { ...provisional, candidateRevision: 'other-revision' },
  ]) {
    const result = await submitSafeApplyOnce(
      gateway({
        async startSafeApply() {
          return { kind: 'accepted', transaction: tx };
        },
      }),
      handle.nodeId,
      command,
    );
    assert.equal(result.kind, 'unknown');
  }
  for (const tx of [
    { ...provisional, id: 'other-transaction' },
    { ...provisional, candidateRevision: 'other-revision' },
  ]) {
    const recovered = await recoverSafeApply(
      gateway({
        async readTransaction() {
          return tx;
        },
      }),
      handle,
    );
    assert.equal(recovered.kind, 'unknown');
  }
});

test('Applied while provisional remains locked; only authoritative confirmation is terminal', () => {
  const shown = presentTransaction(provisional, context);
  assert.equal(shown.state, 'awaiting-confirmation');
  assert.equal(shown.canConfirm, true);
  assert.equal(shown.blocksNewApply, true);
  assert.equal(presentTransaction(confirmed, context).state, 'confirmed');
  assert.equal(presentTransaction(confirmed, context).canConfirm, false);
});

test('local deadline disables decisions but never manufactures rollback or a terminal verdict', () => {
  const expired = presentTransaction(provisional, {
    ...context,
    nowMonotonicMs: 91000,
  });
  assert.equal(expired.remainingSeconds, 0);
  assert.equal(expired.state, 'checking-outcome');
  assert.equal(expired.canConfirm, false);
  assert.equal(expired.canRollback, false);
  assert.equal(expired.blocksNewApply, true);
  assert.equal(provisional.safeApply, 'awaiting-confirmation');
});

test('server time plus monotonic elapsed time works without trusting the wall clock', () => {
  assert.equal(
    presentTransaction(provisional, { ...context, nowMonotonicMs: 16000 })
      .remainingSeconds,
    75,
  );
  const invalid = presentTransaction(
    { ...provisional, confirmationDeadline: 'invalid' },
    context,
  );
  assert.equal(invalid.canConfirm, false);
  assert.equal(invalid.remainingSeconds, null);
  assert.equal(
    presentTransaction(provisional, { ...context, nowMonotonicMs: 500 })
      .canConfirm,
    false,
  );
});

test('disconnect, stale reads, unknown evidence and revoked permissions keep risky actions closed', () => {
  for (const ctx of [
    { ...context, connected: false },
    { ...context, fresh: false },
  ]) {
    const shown = presentTransaction(provisional, ctx);
    assert.equal(shown.canConfirm, false);
    assert.equal(shown.canRollback, false);
    assert.equal(shown.blocksNewApply, true);
  }
  for (const tx of [
    unknown,
    { ...provisional, allowedActions: ['read'] },
    {
      ...provisional,
      evidence: { ...provisional.evidence, health: 'unknown' },
    },
    {
      ...provisional,
      evidence: { ...provisional.evidence, checkpoint: 'unavailable' },
    },
  ])
    assert.equal(presentTransaction(tx, context).canConfirm, false);
  assert.equal(presentTransaction(unknown, context).state, 'checking-outcome');
});

test('inconsistent terminal data, degraded outcomes and rollback conflicts never enable a new apply', () => {
  for (const tx of [
    rollbackConflict,
    { ...provisional, outcome: 'degraded' },
    { ...confirmed, safeApply: 'awaiting-confirmation' },
    {
      ...confirmed,
      evidence: { ...confirmed.evidence, daemonApply: 'unknown' },
    },
  ])
    assert.equal(presentTransaction(tx, context).blocksNewApply, true);
  assert.equal(
    presentTransaction(rollbackConflict, context).state,
    'rollback-conflict',
  );
  assert.equal(
    presentTransaction(rollbackConflict, context).canRollback,
    false,
  );
});

test('a delayed older poll cannot replace a newer terminal result', () => {
  assert.equal(acceptTransactionSnapshot(confirmed, provisional), confirmed);
  assert.equal(acceptTransactionSnapshot(provisional, confirmed), confirmed);
  assert.equal(
    acceptTransactionSnapshot(confirmed, {
      ...provisional,
      sequence: 20,
      requestId: 'other-request',
    }),
    confirmed,
  );
  const heartbeat = acceptTransactionSnapshot(provisional, {
    ...provisional,
    serverTime: '2026-09-05T00:00:15.000Z',
    safeApply: 'confirmed',
  });
  assert.equal(heartbeat.safeApply, 'awaiting-confirmation');
  assert.equal(
    presentTransaction(heartbeat, {
      ...context,
      receivedAtMonotonicMs: 16000,
      nowMonotonicMs: 16000,
    }).remainingSeconds,
    75,
  );
});

test('all four reconciliation outcomes preserve the original transaction and remain distinct', async () => {
  const cases = [
    [provisional, 'awaiting-confirmation', true],
    [
      { ...confirmed, outcome: 'not-applied', safeApply: 'not-started' },
      'not-applied',
      false,
    ],
    [
      { ...unknown, knowledge: 'current', outcome: 'degraded' },
      'degraded',
      true,
    ],
    [
      { ...unknown, knowledge: 'current', outcome: 'needs-attention' },
      'needs-attention',
      true,
    ],
  ];
  for (const [tx, state, locked] of cases) {
    const recovered = await recoverSafeApply(
      gateway({
        async readTransaction() {
          return tx;
        },
      }),
      handle,
    );
    assert.equal(recovered.kind, 'accepted');
    assert.equal(recovered.transaction.id, provisional.id);
    const shown = presentTransaction(recovered.transaction, context);
    assert.equal(shown.state, state);
    assert.equal(shown.blocksNewApply, locked);
  }
});
