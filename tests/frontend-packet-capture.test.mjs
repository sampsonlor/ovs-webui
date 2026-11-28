import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { API } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';
import {
  canCancelCapture,
  captureRoute,
  readVerifiedCapture,
} from '../frontend/src/packet-capture.ts';

const interfaceID = '11111111-1111-4111-8111-111111111111';
const jobID = '22222222-2222-4222-8222-222222222222';
const owner = '33333333-3333-4333-8333-333333333333';
const epoch = '44444444-4444-4444-8444-444444444444';
const profile = 'lldp-ethernet-headers';
const json = (value, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

await test('capture submit requires current approved Interface, elevated desktop session and exact reviewed limits', async () => {
  let session = {
    principal_id: owner,
    csrf_token: 'test-only',
    request_epochs: { management: epoch, workspace: epoch },
    effective_capabilities: ['diagnostics.disruptive', 'jobs.read'],
    elevated_until: new Date(Date.now() - 1000).toISOString(),
  };
  let targets = {
    policy_state: 'enabled',
    targets: [{ id: interfaceID, name: 'fixture' }],
  };
  const posts = [];
  const storage = new Map();
  const api = new API(
    async (path, input) => {
      if (path === '/api/v1/session') return json(session);
      if (path === '/api/v1/captures/targets') return json(targets);
      assert.equal(path, '/api/v1/captures');
      assert.equal(input.method, 'POST');
      const body = JSON.parse(input.body);
      assert.equal(input.headers['Idempotency-Key'], body.request_id);
      posts.push(body);
      return json(
        {
          code: 'FIXTURE',
          request_id: body.request_id,
          request_domain: 'management',
          command_effect: 'not-started',
        },
        422,
      );
    },
    {
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    },
  );
  const controller = new Controller(api, captureRoute);
  await controller.refresh();
  await controller.startCapture(interfaceID, 30, 54, profile, true, true);
  assert.equal(posts.length, 0, 'an expired elevation cannot launch capture');
  session = {
    ...session,
    elevated_until: new Date(Date.now() + 60_000).toISOString(),
  };
  await controller.refresh();
  for (const args of [
    [interfaceID, 30, 54, profile, true, false],
    [interfaceID, 30, 54, profile, false, true],
    [jobID, 30, 54, profile, true, true],
    [interfaceID, 301, 54, profile, true, true],
    [interfaceID, 30, 53, profile, true, true],
    [interfaceID, 30, 1_048_577, profile, true, true],
    [interfaceID, 30, 54, 'unapproved-profile', true, true],
  ])
    await controller.startCapture(...args);
  assert.equal(posts.length, 0);
  const call = controller.startCapture(
    interfaceID,
    30,
    54,
    profile,
    true,
    true,
  );
  await call;
  assert.equal(posts.length, 1);
  assert.deepEqual(posts[0], {
    request_id: posts[0].request_id,
    interface_id: interfaceID,
    duration_seconds: 30,
    max_bytes: 54,
    filter: profile,
  });
  assert.equal(api.pending(), null);
  targets = { policy_state: 'disabled', targets: [] };
  await controller.refresh();
  await controller.startCapture(interfaceID, 30, 54, profile, true, true);
  assert.equal(posts.length, 1, 'disabled policy cannot launch capture');
  const job = {
    id: jobID,
    operation: 'createCapture',
    owner_id: owner,
    state: 'running',
    cancellable: true,
    allowed_actions: ['cancel'],
  };
  const cancelling = {
    ...session,
    effective_capabilities: [...session.effective_capabilities, 'jobs.cancel'],
  };
  assert.equal(canCancelCapture(job, cancelling), true);
  assert.equal(canCancelCapture(job, session), false);
  assert.equal(
    canCancelCapture({ ...job, allowed_actions: [] }, cancelling),
    false,
  );
  assert.equal(
    canCancelCapture({ ...job, state: 'cancel-requested' }, cancelling),
    false,
  );
});

await test('capture artifact rejects malformed bounds, expired data and changed bytes', async () => {
  const bytes = Buffer.alloc(54);
  bytes.writeUInt32LE(0xa1b2c3d4);
  const now = Date.parse('2026-10-20T10:00:00Z');
  const metadata = {
    id: jobID,
    target_ref: { kind: 'interface', id: interfaceID },
    profile,
    state: 'complete',
    packets: 1,
    bytes: bytes.length,
    parts: 1,
    expires_at: new Date(now + 1000).toISOString(),
    sha256: createHash('sha256').update(bytes).digest('hex'),
  };
  function part(data = bytes) {
    return { id: jobID, part: 0, data_base64: data.toString('base64') };
  }
  const verify = (meta, reader = async () => part(), clock = () => now) =>
    readVerifiedCapture(jobID, meta, reader, () => true, clock);
  assert.deepEqual(Buffer.from(await verify(metadata)), bytes);
  await assert.rejects(
    verify({ ...metadata, target_ref: { kind: 'port', id: interfaceID } }),
    /METADATA_INVALID/,
  );
  await assert.rejects(
    verify({ ...metadata, packets: 4097, bytes: 24 + 30 * 4097 }),
    /METADATA_INVALID/,
  );
  await assert.rejects(verify({ ...metadata, bytes: 55 }), /METADATA_INVALID/);
  await assert.rejects(
    verify(metadata, async () => part(Buffer.alloc(54, 1))),
    /CHECKSUM_MISMATCH/,
  );
  await assert.rejects(
    verify(
      metadata,
      async () => part(),
      () => now + 1000,
    ),
    /EXPIRED/,
  );
});

await test('capture acknowledgement keeps the submitted Interface and recovers the original Job', async () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
  let request;
  let wrongInterface = false;
  let posts = 0;
  const api = new API(async (path, input) => {
    if (path === '/api/v1/captures') {
      posts++;
      request = JSON.parse(input.body).request_id;
      return json(
        {
          request_id: request,
          request_domain: 'management',
          request_epoch: epoch,
          job_id: jobID,
          resource_ref: {
            kind: 'interface',
            id: wrongInterface ? owner : interfaceID,
          },
        },
        202,
      );
    }
    assert.match(path, /\/requests\//);
    return json({
      request_id: request,
      request_domain: 'management',
      request_epoch: epoch,
      state: 'accepted',
      resource_ref: { kind: 'interface', id: interfaceID },
      job_ref: { kind: 'job', id: jobID },
    });
  }, storage);
  api.session = { principal_id: owner, request_epochs: { management: epoch } };
  const command = {
    interface_id: interfaceID,
    duration_seconds: 30,
    max_bytes: 54,
    filter: profile,
  };
  assert.equal(
    (await api.command('/captures', 'POST', 'management', command)).job_id,
    jobID,
  );
  assert.equal(api.pending(), null);
  wrongInterface = true;
  await assert.rejects(
    api.command('/captures', 'POST', 'management', command),
    /IDENTITY_MISMATCH/,
  );
  assert.equal(api.pending().request_id, request);
  assert.equal((await api.recover()).job_ref.id, jobID);
  assert.equal(api.pending(), null);
  assert.equal(posts, 2);
});
