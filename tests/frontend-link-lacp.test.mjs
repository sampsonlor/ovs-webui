import assert from 'node:assert/strict';
import { test } from 'node:test';
import { API } from '../frontend/src/api.ts';
import {
  diagnosticsRoute,
  diagnosticResultRoute,
  diagnosticTargets,
  linkLACPPath,
} from '../frontend/src/diagnostics.ts';
import { Controller } from '../frontend/src/model.ts';

const owner = '11111111-1111-4111-8111-111111111111';
const epoch = '22222222-2222-4222-8222-222222222222';
const target = { kind: 'port', id: '33333333-3333-4333-8333-333333333333' };
const job = { kind: 'job', id: '44444444-4444-4444-8444-444444444444' };
const session = {
  principal_id: owner,
  csrf_token: 'fixture',
  effective_capabilities: ['jobs.read', 'inventory.read', 'diagnostics.read'],
  request_epochs: { management: epoch, workspace: epoch },
};
const path = `/api/v1${linkLACPPath}`;
const args = { target_ref: target, sample_seconds: 5, port_limit: 10 };
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

await test('diagnostic acknowledgement and lost reply recover the original Job through the shared receipt', async () => {
  const storage = new Map();
  let submitted;
  let loseReply = false;
  let posts = 0;
  const api = new API(
    async (url, request) => {
      if (url === path && request.method === 'POST') {
        posts++;
        submitted = JSON.parse(request.body);
        assert.equal(request.headers['X-OVS-Request-Epoch'], epoch);
        assert.equal(request.headers['Idempotency-Key'], submitted.request_id);
        assert.deepEqual(
          { ...submitted, request_id: undefined },
          { ...args, request_id: undefined },
        );
        if (loseReply) throw new TypeError('fixture transport lost reply');
        return json(
          {
            request_id: submitted.request_id,
            request_domain: 'management',
            request_epoch: epoch,
            resource_ref: target,
            job_id: job.id,
          },
          202,
        );
      }
      assert.match(url, /\/requests\//);
      return json({
        request_id: submitted.request_id,
        request_domain: 'management',
        request_epoch: epoch,
        state: 'accepted',
        command_effect: 'accepted',
        resource_ref: target,
        job_ref: job,
      });
    },
    {
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    },
  );
  api.session = session;
  assert.equal(
    (await api.command(linkLACPPath, 'POST', 'management', args)).job_id,
    job.id,
  );
  assert.equal(api.pending(), null);
  loseReply = true;
  await assert.rejects(
    api.command(linkLACPPath, 'POST', 'management', args),
    /lost reply/,
  );
  assert.equal(api.pending().request_id, submitted.request_id);
  assert.equal((await api.recover()).job_ref.id, job.id);
  assert.equal(api.pending(), null);
  assert.equal(posts, 2);
});

await test('diagnostic targets and job result require fresh evidence and current diagnostic scope', async () => {
  const inventory = {
    availability: 'complete',
    source: { freshness: 'fresh' },
    items: [{ management_id: target.id, name: 'fixture port' }],
  };
  assert.deepEqual(diagnosticTargets(inventory, 'port'), [
    { ref: target, name: 'fixture port' },
  ]);
  assert.equal(
    diagnosticTargets({ ...inventory, source: { freshness: 'stale' } }, 'port'),
    null,
  );
  assert.equal(
    diagnosticTargets({ ...inventory, availability: 'degraded' }, 'port'),
    null,
  );

  const resultPath = `${path}/${job.id}`;
  const result = {
    template_id: 'diag.net.link-lacp',
    target_ref: target,
    state: 'partial',
    ports: [],
    partial: true,
    truncated: false,
  };
  let jobState = 'running';
  let jobOwner = owner;
  let reads = 0;
  const transport = async (url) => {
    if (url === '/api/v1/session') return json(session);
    if (url === `/api/v1/jobs/${job.id}`)
      return json({
        id: job.id,
        operation: 'runLinkLACPDiagnostic',
        owner_id: jobOwner,
        state: jobState,
      });
    assert.equal(url, resultPath);
    reads++;
    return json(result);
  };
  const storage = { getItem: () => null };
  const c = new Controller(
    new API(transport, storage),
    `/operations/jobs/${job.id}`,
  );
  await c.refresh();
  assert.equal(reads, 0);
  jobState = 'succeeded';
  await c.refresh();
  assert.deepEqual(c.state.linkLACPResult.value, result);
  jobOwner = epoch;
  await c.refresh();
  assert.equal(c.state.linkLACPResult.value, null);
  assert.equal(reads, 1);

  const directSession = {
    ...session,
    effective_capabilities: ['diagnostics.read', 'inventory.read'],
  };
  const direct = diagnosticResultRoute(job.id, directSession);
  assert.equal(direct, `${diagnosticsRoute}?job_id=${job.id}`);
  const d = new Controller(
    new API(
      async (url) =>
        url === '/api/v1/session' ? json(directSession) : transport(url),
      storage,
    ),
    direct,
  );
  await d.refresh();
  assert.deepEqual(d.state.linkLACPResult.value, result);
});
