import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  Job,
  LinkLACPDiagnosticCommand,
  LinkLACPDiagnosticResult,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native Go/OVS frontend fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  isolation: { name: string; management_id: string };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

test('Link and LACP observes an exact native Port through one durable Job', async ({
  page,
}) => {
  const diagnostic = '/api/v1/diagnostics/link-lacp';
  const target = fixture.isolation;
  const account = 'browser-interfaces';
  expect(
    (await page.goto(fixture.origin + '/operations/diagnostics'))?.status(),
  ).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const signedIn = page.waitForResponse(
    (reply) =>
      reply.request().method() === 'POST' &&
      reply.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await signedIn).status()).toBe(201);

  await expect(
    page.getByRole('heading', { name: 'Diagnostics', exact: true }),
  ).toBeVisible();
  await page.getByLabel('Target type', { exact: true }).selectOption('port');
  await page.getByLabel('Target name', { exact: true }).fill(target.name);
  await page.getByRole('button', { name: 'Find targets', exact: true }).click();
  const port = page.getByLabel('Port', { exact: true });
  await expect(
    port.getByRole('option', { name: target.name, exact: true }),
  ).toHaveAttribute('value', target.management_id);
  await port.selectOption(target.management_id);
  await expect(page.getByLabel('Maximum ports', { exact: true })).toHaveValue(
    '10',
  );
  await page.getByLabel('Maximum ports', { exact: true }).selectOption('1');
  await page
    .getByLabel('Observation budget', { exact: true })
    .selectOption({ label: '10 seconds' });

  const posts: LinkLACPDiagnosticCommand[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith(diagnostic))
      posts.push(request.postDataJSON() as LinkLACPDiagnosticCommand);
  });
  const posted = page.waitForResponse(
    (reply) =>
      reply.request().method() === 'POST' && reply.url().endsWith(diagnostic),
  );
  await page
    .getByRole('button', { name: 'Run diagnostic', exact: true })
    .click();
  const acceptedReply = await posted;
  expect(acceptedReply.status()).toBe(202);
  const accepted: Accepted = await acceptedReply.json();
  expect(accepted.resource_ref).toEqual({
    kind: 'port',
    id: target.management_id,
  });
  expect(posts).toEqual([
    {
      request_id: accepted.request_id,
      target_ref: accepted.resource_ref,
      sample_seconds: 10,
      port_limit: 1,
    },
  ]);
  expect(await acceptedReply.request().headerValue('Idempotency-Key')).toBe(
    accepted.request_id,
  );

  const jobPath = `/operations/jobs/${accepted.job_id}`;
  await expect(page).toHaveURL(fixture.origin + jobPath);
  const panel = page.getByRole('region', {
    name: 'Link and LACP result',
    exact: true,
  });
  await expect(
    panel.getByText('Configured LACP', { exact: false }),
  ).toBeVisible();
  const [jobResponse, resultResponse] = await Promise.all([
    page.request.get(fixture.origin + `/api/v1/jobs/${accepted.job_id}`),
    page.request.get(fixture.origin + `${diagnostic}/${accepted.job_id}`),
  ]);
  expect(jobResponse.status()).toBe(200);
  expect(resultResponse.status()).toBe(200);
  const job: Job = await jobResponse.json();
  const result: LinkLACPDiagnosticResult = await resultResponse.json();
  expect(job).toMatchObject({
    id: accepted.job_id,
    operation: 'runLinkLACPDiagnostic',
    state: 'succeeded',
    resource_ref: accepted.resource_ref,
  });
  expect(result).toMatchObject({
    template_id: 'diag.net.link-lacp',
    target_ref: accepted.resource_ref,
    authority: 'ovsdb-structured',
    truncated: false,
  });
  expect(['complete', 'partial']).toContain(result.state);
  expect(result.ports).toHaveLength(1);
  expect(result.ports[0].port_ref).toEqual(accepted.resource_ref);
  expect(result.ports[0].members).toHaveLength(1);
  await expect(
    panel.getByRole('columnheader', { name: 'LACP current' }),
  ).toBeVisible();

  expect((await page.reload())?.status()).toBe(200);
  await expect(
    panel.getByText('Configured LACP', { exact: false }),
  ).toBeVisible();
  expect(posts).toHaveLength(1);
});
