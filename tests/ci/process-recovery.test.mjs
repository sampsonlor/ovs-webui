import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { randomUUID } from 'node:crypto';
import { CoreHttpClient } from '../../lib/api/http-client.ts';
import { validateContract } from '../../lib/api/validator.generated.mjs';

async function environment(t, paused = false) {
  const root = resolve(tmpdir());
  const directory = mkdtempSync(join(root, 'ovs-ci-'));
  const children = [];
  const stop = async (child, signal = 'SIGTERM') => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    const exited = once(child, 'exit');
    child.kill(signal);
    const force = setTimeout(() => child.kill('SIGKILL'), 3000);
    try {
      await exited;
    } finally {
      clearTimeout(force);
    }
  };
  t.after(async () => {
    for (const child of children) await stop(child);
    if (
      dirname(resolve(directory)) !== root ||
      !directory.startsWith(join(root, 'ovs-ci-'))
    )
      throw new Error('Cleanup target escaped the isolated test root.');
    rmSync(directory, { recursive: true, force: true });
  });
  async function start(pause = false) {
    const child = spawn(
      process.execPath,
      [
        '--experimental-strip-types',
        'scripts/start-core-test-server.mjs',
        ...(pause ? ['--pause-validation'] : []),
      ],
      {
        cwd: resolve('.'),
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
        env: {
          ...process.env,
          OVS_TEST_SERVER: '1',
          OVS_TEST_DATA_DIR: directory,
        },
      },
    );
    children.push(child);
    let stderr = '';
    child.stderr.on('data', (chunk) => {
      stderr = (stderr + chunk.toString()).slice(-8000);
    });
    const lines = createInterface({ input: child.stdout });
    const origin = await new Promise((resolveReady, reject) => {
      const timer = setTimeout(
        () => finish(new Error(`Test service startup timed out. ${stderr}`)),
        10_000,
      );
      const onError = (error) => finish(error);
      const onExit = () =>
        finish(new Error(`Test service exited before ready. ${stderr}`));
      const onLine = (line) => {
        try {
          const data = JSON.parse(line);
          if (
            data.ready === true &&
            /^http:\/\/127\.0\.0\.1:\d+$/.test(data.origin)
          )
            finish(null, data.origin);
        } catch {
          /* Node warnings are not readiness evidence. */
        }
      };
      function finish(error, value) {
        clearTimeout(timer);
        child.off('error', onError);
        child.off('exit', onExit);
        lines.off('line', onLine);
        lines.close();
        if (error) reject(error);
        else resolveReady(value);
      }
      child.once('error', onError);
      child.once('exit', onExit);
      lines.on('line', onLine);
    });
    return { child, origin };
  }
  return { ...(await start(paused)), start, stop };
}

async function login(origin, principal = 'alice') {
  const response = await fetch(`${origin}/__ovs_lab/session`, {
    method: 'POST',
    signal: AbortSignal.timeout(5000),
    headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: JSON.stringify({ principal }),
  });
  assert.equal(response.status, 200);
  return {
    session: await response.json(),
    cookie: response.headers.get('set-cookie').split(';')[0],
  };
}
function client(origin, identity) {
  return new CoreHttpClient({
    origin,
    nodeId: identity.session.nodeId,
    validate: validateContract,
    csrfToken: () => identity.session.csrfToken,
    timeoutMs: 5000,
    fetch: (url, options) => {
      const headers = new Headers(options.headers);
      headers.set('Cookie', identity.cookie);
      headers.set('Origin', origin);
      headers.set('X-OVS-Lab-Epoch', identity.session.epoch);
      return fetch(url, { ...options, headers });
    },
  });
}
async function stage(api) {
  const snapshot = await api.readCandidate();
  const result = await api.mutateCandidate(
    {
      requestId: randomUUID(),
      operation: 'set-vlan',
      portId: 'port-2',
      mine: { mode: 'trunk', tag: null, trunks: [120, 240] },
      expectedGeneration: snapshot.candidate.currentGeneration,
    },
    snapshot,
  );
  assert.equal(result.kind, 'accepted');
  return result.snapshot.candidate;
}
async function terminalValidation(api, id) {
  for (let attempt = 0; attempt < 40; attempt++) {
    const result = await api.readValidation(id);
    if (!['pending', 'running'].includes(result.status)) return result;
    await delay(100);
  }
  assert.fail('Server worker did not finish within its bounded test window.');
}

test(
  'isolated HTTP service enforces identity and safety prerequisites using a fresh database',
  { timeout: 20_000 },
  async (t) => {
    const env = await environment(t);
    const identity = await login(env.origin);
    const api = client(env.origin, identity);
    const candidate = await stage(api);
    const accepted = await api.validateCandidate({
      requestId: randomUUID(),
      candidateId: candidate.id,
      expectedCandidateRevision: candidate.revision,
      expectedGeneration: candidate.currentGeneration,
    });
    assert.equal(accepted.kind, 'accepted');
    const validation = await terminalValidation(api, accepted.validation.id);
    assert.equal(validation.status, 'blocked');
    assert.equal(validation.safetyPlan.checkpoint, 'unavailable');
    assert.equal((await api.readValidationJob(validation)).state, 'succeeded');
    assert.equal((await api.readWorkspace()).permissions.startSafeApply, false);
    const observer = client(env.origin, await login(env.origin, 'observer'));
    assert.equal((await observer.readCandidate()).candidate.intents.length, 0);
    assert.equal((await observer.readWorkspace()).permissions.validate, false);
    await assert.rejects(observer.readValidation(validation.id));
    await env.stop(env.child);
  },
);

test(
  'a killed server restores its session, Candidate, accepted request and queued validation without resubmission',
  { timeout: 25_000 },
  async (t) => {
    const env = await environment(t, true);
    const identity = await login(env.origin);
    const api = client(env.origin, identity);
    const candidate = await stage(api);
    const observation = await fetch(`${env.origin}/__ovs_lab/observations`, {
      method: 'POST',
      signal: AbortSignal.timeout(5000),
      headers: {
        Origin: env.origin,
        Cookie: identity.cookie,
        'Content-Type': 'application/json',
        'X-CSRF-Token': identity.session.csrfToken,
        'X-OVS-Lab-Epoch': identity.session.epoch,
      },
      body: JSON.stringify({ scenario: 'safety-available' }),
    });
    assert.equal(observation.status, 200);
    const requestId = randomUUID();
    const accepted = await api.validateCandidate({
      requestId,
      candidateId: candidate.id,
      expectedCandidateRevision: candidate.revision,
      expectedGeneration: candidate.currentGeneration,
    });
    assert.equal(accepted.kind, 'accepted');
    assert.equal(accepted.validation.status, 'pending');
    await env.stop(env.child, 'SIGKILL');
    const restarted = await env.start();
    const restoredApi = client(restarted.origin, identity);
    assert.deepEqual((await restoredApi.readCandidate()).candidate, candidate);
    const validation = await terminalValidation(
      restoredApi,
      accepted.validation.id,
    );
    assert.equal(validation.status, 'passed');
    assert.equal(validation.requestId, requestId);
    assert.deepEqual(validation.diff, candidate.intents);
    assert.equal(
      (await restoredApi.readRequest(requestId)).validationId,
      validation.id,
    );
    assert.equal(
      (await restoredApi.readWorkspace()).latestValidation.id,
      validation.id,
    );
    assert.equal(
      (await restoredApi.readWorkspace()).permissions.startSafeApply,
      false,
    );
    // A fresh second server/database represents another CI run, not a shared staging database.
    const other = await environment(t);
    const otherApi = client(other.origin, await login(other.origin));
    assert.equal((await otherApi.readCandidate()).candidate.intents.length, 0);
    assert.equal(await otherApi.readRequest(requestId), null);
    await env.stop(restarted.child);
  },
);
