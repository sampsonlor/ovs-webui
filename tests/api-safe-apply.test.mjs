import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { resolve, join, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { CoreLabStore } from '../dev/core-lab-store.mjs';
import { validateContract } from '../lib/api/validator.generated.mjs';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { coreLabMiddleware } from '../dev/core-lab-plugin.mjs';
import { CoreHttpClient } from '../lib/api/http-client.ts';
import {
  WorkspaceController,
  parseTransactionRecoveryHint,
} from '../lib/api/workspace-controller.ts';

function fixture(t) {
  const root = resolve(tmpdir());
  const directory = mkdtempSync(join(root, 'ovs-safe-test-'));
  let time = Date.now();
  const stores = [];
  const open = () => {
    const store = new CoreLabStore(join(directory, 'state.sqlite'), {
      clock: () => time,
    });
    stores.push(store);
    return store;
  };
  t.after(() => {
    for (const store of stores) {
      try {
        store.close();
      } catch {
        /* Closed for restart. */
      }
    }
    if (dirname(resolve(directory)) !== root)
      throw new Error('Cleanup escaped its test root.');
    rmSync(directory, { recursive: true, force: true });
  });
  return {
    open,
    advance: (milliseconds) => {
      time += milliseconds;
    },
  };
}
function prepared(store, principal = 'alice') {
  const snapshot = store.snapshot(principal);
  const session = { principal, nodeId: snapshot.candidate.nodeId };
  const edit = {
    requestId: randomUUID(),
    operation: 'set-vlan',
    portId: 'port-2',
    mine: { mode: 'trunk', tag: null, trunks: [120, 240] },
    expectedGeneration: snapshot.candidate.currentGeneration,
  };
  const candidate = store.mutate(session, edit, snapshot.etag, edit.requestId);
  assert.equal(candidate.status, 200);
  if (store.meta('validationPolicy').safety !== 'available')
    store.validations.policyChange('available');
  const validate = {
    requestId: randomUUID(),
    candidateId: candidate.body.id,
    expectedCandidateRevision: candidate.body.revision,
    expectedGeneration: candidate.body.currentGeneration,
  };
  const accepted = store.validations.start(
    session,
    validate,
    validate.requestId,
  );
  assert.equal(accepted.status, 202);
  store.validations.tick();
  store.validations.tick();
  assert.equal(
    store.validations.read(principal, accepted.body.id).body.status,
    'passed',
  );
  return {
    session,
    before:
      edit.portId &&
      store.meta('inventory').items.find((port) => port.id === edit.portId)
        .configuration,
    command: {
      ...validate,
      requestId: randomUUID(),
      validationId: accepted.body.id,
      reason: 'Review the synthetic VLAN change.',
    },
  };
}
function start(store, setup) {
  const reply = store.transactions.start(
    setup.session,
    setup.command,
    setup.command.requestId,
  );
  assert.equal(reply.status, 202, JSON.stringify(reply.body));
  assert.equal(validateContract('TransactionResource', reply.body), true);
  return reply.body;
}
function awaiting(store, setup) {
  const tx = start(store, setup);
  for (let i = 0; i < 4; i++) store.transactions.tick();
  const result = store.transactions.read(setup.session.principal, tx.id).body;
  assert.equal(result.safeApply, 'awaiting-confirmation');
  return result;
}
function decision(
  store,
  setup,
  tx,
  choice = 'confirm',
  requestId = randomUUID(),
) {
  const command = {
    requestId,
    expectedTransactionSequence: tx.sequence,
    decision: choice,
    reason: `Review and ${choice} this synthetic change.`,
  };
  return {
    command,
    reply: store.transactions.decide(
      setup.session,
      tx.id,
      command,
      command.requestId,
    ),
  };
}

void test('admission, checkpoint, commit receipt and confirmation window survive every server boundary', (t) => {
  const f = fixture(t);
  let store = f.open();
  const setup = prepared(store);
  const tx = start(store, setup);
  assert.equal(store.transactions.blocked(), true);
  assert.equal(store.candidate('alice').lockedByTransactionId, tx.id);
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
  let sequence = tx.sequence;
  let deadline = null;
  for (const phase of [
    'preflight',
    'committing',
    'applying',
    'awaiting-confirmation',
  ]) {
    store.close();
    store = f.open();
    store.transactions.tick();
    const restored = store.transactions.read('alice', tx.id).body;
    assert.equal(restored.phase, phase);
    assert.ok(restored.sequence > sequence);
    sequence = restored.sequence;
    assert.equal(validateContract('TransactionResource', restored), true);
    if (deadline) assert.equal(restored.confirmationDeadline, deadline);
    deadline = restored.confirmationDeadline;
    if (phase === 'preflight' || phase === 'committing')
      assert.deepEqual(
        store.meta('inventory').items[1].configuration,
        setup.before,
      );
  }
  assert.equal(store.workspace(setup.session).latestTransaction.id, tx.id);
  assert.equal(store.candidate('alice').freshness, 'current');
  assert.equal(
    store.readRequest('alice', setup.command.requestId).body.transactionId,
    tx.id,
  );
  assert.equal(store.transactions.read('bob', tx.id).status, 404);
  assert.equal(store.transactions.evidence('bob', tx.id).status, 404);
  assert.equal(store.transactions.job('bob', tx.jobId), null);
});

void test('confirmation consumes Candidate only after authoritative application and survives lost-ack replay', (t) => {
  const f = fixture(t);
  let store = f.open();
  const setup = prepared(store);
  const tx = awaiting(store, setup);
  assert.equal(store.candidate('alice').intents.length, 1);
  assert.equal(store.transactions.job('alice', tx.jobId).body.state, 'running');
  const { command, reply } = decision(store, setup, tx);
  assert.equal(reply.status, 202);
  assert.equal(reply.body.safeApply, 'confirmed');
  assert.equal(store.candidate('alice').intents.length, 0);
  assert.equal(store.transactions.blocked(), false);
  store.close();
  store = f.open();
  f.advance(100_000);
  const repeated = store.transactions.decide(
    setup.session,
    tx.id,
    command,
    command.requestId,
  );
  assert.deepEqual(repeated, reply);
  const original = store.transactions.start(
    setup.session,
    setup.command,
    setup.command.requestId,
  );
  assert.equal(original.body.id, tx.id);
  assert.equal(
    store.db.prepare('SELECT count(*) AS count FROM safe_transactions').get()
      .count,
    1,
  );
  assert.equal(
    store.transactions.start(
      setup.session,
      { ...setup.command, reason: 'Changed request' },
      setup.command.requestId,
    ).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
  const evidence = store.transactions.evidence('alice', tx.id).body;
  assert.equal(validateContract('EvidencePage', evidence), true);
  assert.equal(
    evidence.items.filter((entry) => entry.code === 'CONFIRMED').length,
    1,
  );
  assert.equal(
    evidence.items.find((entry) => entry.code === 'CONFIRMED').requestId,
    command.requestId,
  );
  assert.equal(
    store.workspace(setup.session).latestTransaction.safeApply,
    'confirmed',
  );
});

void test('deadline wins a late confirm and server rollback preserves intent without browser participation', (t) => {
  const f = fixture(t);
  let store = f.open();
  const setup = prepared(store);
  const tx = awaiting(store, setup);
  f.advance(90_000);
  store.close();
  store = f.open();
  const late = decision(store, setup, tx);
  assert.equal(late.reply.body.code, 'DECISION_EXPIRED');
  store.transactions.tick();
  const result = store.transactions.read('alice', tx.id).body;
  assert.equal(result.safeApply, 'rolled-back');
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
  assert.equal(store.transactions.blocked(), false);
  assert.equal(store.candidate('alice').intents.length, 1);
  assert.equal(store.candidate('alice').freshness, 'stale');
  assert.equal(
    store.workspace(setup.session).latestValidation.status,
    'expired',
  );
  assert.equal(
    store.transactions.decide(
      setup.session,
      tx.id,
      late.command,
      late.command.requestId,
    ).body.code,
    'DECISION_EXPIRED',
  );
});

void test('rollback compares owned fields and a conflict cannot be cleared by healthy fixtures or read-only reconciliation', (t) => {
  const f = fixture(t);
  const store = f.open();
  const setup = prepared(store);
  const tx = awaiting(store, setup);
  const foreign = { mode: 'access', tag: 777, trunks: [] };
  store.externalChange('port-2', foreign);
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.outcome,
    'needs-attention',
  );
  f.advance(90_000);
  store.transactions.tick();
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.safeApply,
    'rollback-conflict',
  );
  store.setMeta('nodeBlocked', false);
  store.setMeta('providerAvailable', true);
  const command = { requestId: randomUUID() };
  const accepted = store.transactions.reconcile(
    setup.session,
    tx.id,
    command,
    command.requestId,
  );
  store.transactions.tick();
  assert.equal(
    store.transactions.job('alice', accepted.body.id).body.state,
    'succeeded',
  );
  assert.equal(
    store.transactions.read('alice', tx.id).body.outcome,
    'needs-attention',
  );
  assert.equal(store.transactions.blocked(), true);
  assert.deepEqual(
    store.meta('inventory').items[1].configuration.value,
    foreign,
  );
  assert.equal(
    store.transactions.reconcile(
      setup.session,
      tx.id,
      { requestId: randomUUID(), result: 'applied' },
      'request-invalid',
    ).status,
    422,
  );
});

void test('protected rollback preserves an unrelated external Port change', (t) => {
  const f = fixture(t);
  const store = f.open();
  const setup = prepared(store);
  const tx = awaiting(store, setup);
  const accepted = decision(store, setup, tx, 'rollback');
  assert.equal(accepted.reply.status, 202);
  const foreign = { mode: 'access', tag: 888, trunks: [] };
  store.externalChange('port-3', foreign);
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.safeApply,
    'rolled-back',
  );
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
  assert.deepEqual(
    store.meta('inventory').items[2].configuration.value,
    foreign,
  );
});

void test('provider loss retains OutcomeUnknown and the watchdog resumes after restart and recovery', (t) => {
  const f = fixture(t);
  let store = f.open();
  const setup = prepared(store);
  const tx = awaiting(store, setup);
  store.setMeta('providerAvailable', false);
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.knowledge,
    'outcome-unknown',
  );
  assert.deepEqual(
    store.transactions.read('alice', tx.id).body.allowedActions,
    ['read', 'reconcile'],
  );
  f.advance(90_000);
  store.transactions.tick();
  store.transactions.tick();
  assert.equal(store.transactions.blocked(), true);
  store.close();
  store = f.open();
  store.setMeta('providerAvailable', true);
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.safeApply,
    'rolled-back',
  );
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
});

void test('node admission excludes another user across database connections; stale decisions and revoked permissions cannot confirm', (t) => {
  const f = fixture(t);
  const first = f.open();
  const a = prepared(first);
  const b = prepared(first, 'bob');
  const second = f.open();
  const tx = awaiting(first, a);
  assert.equal(
    second.transactions.start(b.session, b.command, b.command.requestId).body
      .code,
    'TRANSACTION_ACTIVE',
  );
  assert.equal(second.workspace(b.session).nodeWriteBlocked, true);
  assert.equal(second.workspace(b.session).activeTransactions.length, 0);
  assert.equal(
    decision(second, a, { ...tx, sequence: tx.sequence - 1 }).reply.body.code,
    'TRANSACTION_VERSION_CHANGED',
  );
  first.setMeta('revokedEditors', ['alice']);
  assert.equal(decision(second, a, tx).reply.body.code, 'FORBIDDEN');
  second.transactions.tick();
  const revoked = second.transactions.read('alice', tx.id).body;
  assert.deepEqual(revoked.allowedActions, ['read']);
  first.setMeta('revokedEditors', []);
  second.transactions.tick();
  const restored = second.transactions.read('alice', tx.id).body;
  assert.ok(restored.sequence > revoked.sequence);
  assert.ok(restored.allowedActions.includes('confirm'));
  assert.equal(restored.confirmationDeadline, tx.confirmationDeadline);
  f.advance(90_000);
  second.transactions.tick();
  second.transactions.tick();
  assert.equal(
    first.transactions.read('alice', tx.id).body.safeApply,
    'rolled-back',
  );
});

void test('expired validation, changed policy and unavailable safety never admit a write; changes after admission fail preflight', (t) => {
  const f = fixture(t);
  const store = f.open();
  const setup = prepared(store);
  store.validations.policyChange('unknown');
  assert.equal(
    store.transactions.start(
      setup.session,
      setup.command,
      setup.command.requestId,
    ).body.code,
    'VALIDATION_EXPIRED',
  );
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
  assert.equal(
    store.readRequest('alice', setup.command.requestId).body.state,
    'rejected',
  );
  const next = prepared(store);
  const tx = start(store, next);
  store.externalChange(null, null);
  store.transactions.tick();
  store.transactions.tick();
  assert.equal(
    store.transactions.read('alice', tx.id).body.outcome,
    'not-applied',
  );
  assert.equal(
    store.transactions.read('alice', tx.id).body.evidence.databaseCommit,
    'not-committed',
  );
  assert.equal(store.transactions.blocked(), false);
  assert.deepEqual(
    store.meta('inventory').items[1].configuration,
    setup.before,
  );
});

async function httpClient(
  t,
  store,
  intercept = async (_url, _options, response) => response,
) {
  const server = createServer(coreLabMiddleware(store));
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => {
    server.closeAllConnections();
    return new Promise((resolve) => server.close(resolve));
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const identity = store.login('alice');
  return new CoreHttpClient({
    origin,
    nodeId: identity.session.nodeId,
    csrfToken: () => identity.session.csrfToken,
    validate: validateContract,
    fetch: async (url, options) => {
      const headers = new Headers(options.headers);
      headers.set('Origin', origin);
      headers.set('Cookie', `ovs_lab_session=${identity.token}`);
      headers.set('X-OVS-Lab-Epoch', identity.session.epoch);
      const response = await fetch(url, { ...options, headers });
      return intercept(url, options, response);
    },
  });
}

void test('HTTP lost Apply and Confirm acknowledgements recover across controller reload without a second POST', async (t) => {
  const f = fixture(t);
  const store = f.open();
  prepared(store);
  let applyPosts = 0;
  let decisionPosts = 0;
  const client = await httpClient(t, store, async (url, options, response) => {
    if (options.method === 'POST' && url.endsWith('/transactions')) {
      applyPosts++;
      await response.json();
      throw new Error('Lost Apply acknowledgement');
    }
    if (options.method === 'POST' && url.endsWith('/decisions')) {
      decisionPosts++;
      await response.json();
      throw new Error('Lost Confirm acknowledgement');
    }
    return response;
  });
  let saved = null;
  const hints = {
    read: () => parseTransactionRecoveryHint(saved, client.nodeId),
    write: (hint) => {
      saved = hint ? JSON.stringify(hint) : null;
    },
  };
  let controller = new WorkspaceController(client, hints);
  await controller.refresh();
  assert.equal(controller.canStartSafeApply(), true);
  assert.equal(
    await controller.startSafeApply('Apply the reviewed VLAN.', randomUUID()),
    false,
  );
  assert.equal(controller.getSnapshot().phase, 'unknown');
  assert.deepEqual(Object.keys(JSON.parse(saved)).sort(), [
    'nodeId',
    'operation',
    'requestId',
  ]);
  assert.equal(
    await controller.startSafeApply('Never resend.', randomUUID()),
    false,
  );
  controller.dispose();
  for (let i = 0; i < 4; i++) store.transactions.tick();
  controller = new WorkspaceController(client, hints);
  await controller.refresh();
  assert.equal(controller.getSnapshot().phase, 'ready');
  assert.equal(
    controller.getSnapshot().transaction.safeApply,
    'awaiting-confirmation',
  );
  assert.equal(controller.transactionPresentation().canConfirm, true);
  assert.equal(saved, null);
  assert.equal(applyPosts, 1);
  assert.equal(
    await controller.decideSafeApply(
      'confirm',
      'Management connectivity verified.',
      randomUUID(),
    ),
    false,
  );
  assert.equal(controller.getSnapshot().phase, 'unknown');
  controller.dispose();
  controller = new WorkspaceController(client, hints);
  await controller.refresh();
  assert.equal(controller.getSnapshot().transaction.safeApply, 'confirmed');
  assert.equal(controller.getSnapshot().snapshot.candidate.intents.length, 0);
  assert.equal(controller.getSnapshot().transactionJob.state, 'succeeded');
  assert.equal(
    controller
      .getSnapshot()
      .evidence.items.some((entry) => entry.code === 'CONFIRMED'),
    true,
  );
  assert.equal(decisionPosts, 1);
  assert.equal(applyPosts, 1);
  assert.equal(saved, null);
  controller.dispose();
});

void test('missing acceptance evidence and unwritable recovery storage keep new submissions closed', async (t) => {
  const store = fixture(t).open();
  prepared(store);
  let writes = 0;
  const client = await httpClient(t, store, async (_url, options, response) => {
    if (options.method === 'POST') writes++;
    return response;
  });
  const hint = {
    requestId: randomUUID(),
    nodeId: client.nodeId,
    operation: 'safe-apply',
  };
  const recovering = new WorkspaceController(client, {
    read: () => hint,
    write: () => assert.fail('Unresolved evidence must retain its hint.'),
  });
  await recovering.refresh();
  assert.equal(recovering.getSnapshot().phase, 'unknown');
  assert.equal(
    await recovering.startSafeApply('Must remain blocked.', randomUUID()),
    false,
  );
  assert.equal(writes, 0);
  recovering.dispose();
  const noStorage = new WorkspaceController(client, {
    read: () => null,
    write: () => {
      throw new Error('Storage is unavailable.');
    },
  });
  await noStorage.refresh();
  assert.equal(noStorage.canStartSafeApply(), true);
  assert.equal(
    await noStorage.startSafeApply('Cannot preserve recovery.', randomUUID()),
    false,
  );
  assert.equal(noStorage.getSnapshot().phase, 'error');
  assert.equal(writes, 0);
  assert.throws(() =>
    parseTransactionRecoveryHint(
      JSON.stringify({ ...hint, nodeId: 'another-node' }),
      client.nodeId,
    ),
  );
  noStorage.dispose();
});
