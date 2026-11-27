import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  InterfaceCounterDiagnosticCommand,
  InterfaceCounterDiagnosticResult,
  Job,
  Port,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native Go/OVS frontend fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  isolation: { name: string; management_id: string };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

test('Active Interface counters follow the original Port and Job', async ({ page }) => {
  test.setTimeout(45_000);
  const endpoint = '/api/v1/diagnostics/interface-counters';
  const account = 'browser-qinq';
  const target = fixture.isolation;
  expect((await page.goto(fixture.origin + '/operations/diagnostics'))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page.getByLabel('Password', { exact: true }).fill(fixture.accounts[account]);
  const signedIn = page.waitForResponse(
    (reply) => reply.request().method() === 'POST' && reply.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await signedIn).status()).toBe(201);
  await page.getByLabel('Diagnostic template', { exact: true }).selectOption('interface-counters');
  await expect(page.getByLabel('Target type', { exact: true })).toHaveCount(0);
  await page.getByLabel('Target name', { exact: true }).fill(target.name);
  await page.getByRole('button', { name: 'Find targets', exact: true }).click();
  const port = page.getByLabel('Port', { exact: true });
  await expect(port.getByRole('option', { name: target.name, exact: true })).toHaveAttribute('value', target.management_id);
  await port.selectOption(target.management_id);
  await page.getByLabel('Sample budget', { exact: true }).selectOption('10');
  await expect(page.getByLabel('Maximum ports', { exact: true })).toHaveCount(0);

  const posts: InterfaceCounterDiagnosticCommand[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith(endpoint))
      posts.push(request.postDataJSON() as InterfaceCounterDiagnosticCommand);
  });
  const posted = page.waitForResponse(
    (reply) => reply.request().method() === 'POST' && reply.url().endsWith(endpoint),
  );
  await page.getByRole('button', { name: 'Run diagnostic', exact: true }).click();
  const acceptedReply = await posted;
  expect(acceptedReply.status()).toBe(202);
  const accepted: Accepted = await acceptedReply.json();
  expect(accepted.resource_ref).toEqual({ kind: 'port', id: target.management_id });
  expect(posts).toEqual([{
    request_id: accepted.request_id,
    target_ref: accepted.resource_ref,
    sample_seconds: 10,
  }]);
  expect(await acceptedReply.request().headerValue('Idempotency-Key')).toBe(accepted.request_id);
  await expect(page).toHaveURL(fixture.origin + `/operations/jobs/${accepted.job_id}`);

  const resultURL = fixture.origin + `${endpoint}/${accepted.job_id}`;
  const panel = page.getByRole('region', { name: 'Interface counter result', exact: true });
  await expect(panel.getByText('Observed Port', { exact: true })).toBeVisible({ timeout: 25_000 });
  await expect(panel.getByText('First sample', { exact: true })).toBeVisible();
  await expect(panel.getByText('Second sample', { exact: true })).toBeVisible();
  const [reply, jobReply, portReply] = await Promise.all([
    page.request.get(resultURL),
    page.request.get(fixture.origin + `/api/v1/jobs/${accepted.job_id}`),
    page.request.get(fixture.origin + `/api/v1/ports/${target.management_id}`),
  ]);
  expect(reply.status()).toBe(200);
  expect(jobReply.status()).toBe(200);
  expect(portReply.status()).toBe(200);
  const job: Job = await jobReply.json();
  const currentPort: Port = await portReply.json();
  expect(job).toMatchObject({
    id: accepted.job_id,
    state: 'succeeded',
    operation: 'runInterfaceCounterDiagnostic',
    resource_ref: accepted.resource_ref,
  });
  const result: InterfaceCounterDiagnosticResult = await reply.json();
  expect(result).toMatchObject({
    template_id: 'diag.host.interface-counters',
    target_ref: accepted.resource_ref,
    source: 'ovsdb-statistics',
    instance_generation: currentPort.instance_generation,
    freshness: 'fresh',
  });
  expect(['complete', 'partial']).toContain(result.state);
  expect(currentPort.interface_refs).toHaveLength(1);
  expect(result.members.map(({ interface_ref }) => interface_ref)).toEqual(currentPort.interface_refs);
  const firstSample = Date.parse(result.first_sampled_at ?? '');
  const secondSample = Date.parse(result.second_sampled_at ?? '');
  expect(Number.isFinite(firstSample)).toBe(true);
  expect(secondSample).toBeGreaterThan(firstSample);
  expect(Object.values(result.members[0].deltas).some(
    (value) => typeof value === 'string' && /^(0|[1-9][0-9]{0,19})$/.test(value),
  )).toBe(true);

  // A mismatched reply must clear the displayed result, while the same
  // durable Job remains selected and no command is replayed.
  await page.route(resultURL, (route) =>
    route.fulfill({ json: { ...result, target_ref: { kind: 'port', id: '00000000-0000-4000-8000-000000000009' } } }),
  );
  await page.getByRole('button', { name: 'Refresh resource', exact: true }).click();
  await expect(panel.getByText('The result could not be verified for the original Port.')).toBeVisible();
  await expect(panel.getByText('Observed Port', { exact: true })).toHaveCount(0);
  await expect(page).toHaveURL(fixture.origin + `/operations/jobs/${accepted.job_id}`);
  expect(posts).toHaveLength(1);
});
