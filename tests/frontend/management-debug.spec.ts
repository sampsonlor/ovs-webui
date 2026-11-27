import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  Job,
  ResourcePage,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: { origin: string; accounts: Record<string, string> } =
  JSON.parse(readFileSync(fixturePath, 'utf8'));

function post(page: Page, endpoint: string) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url() === fixture.origin + '/api/v1' + endpoint,
  );
}

async function verifyEvidence(page: Page, accepted: Accepted, result: string) {
  const jobReply = await page.request.get(
    fixture.origin + '/api/v1/jobs/' + accepted.job_id,
  );
  expect(jobReply.status()).toBe(200);
  const job: Job = await jobReply.json();
  expect(job).toMatchObject({
    id: accepted.job_id,
    operation: 'setManagementDebug',
    state: 'succeeded',
    correlation_id: accepted.correlation_id,
    reason_code: result,
  });
  const auditReply = await page.request.get(
    fixture.origin + '/api/v1/audit?job_id=' + accepted.job_id,
  );
  expect(auditReply.status()).toBe(200);
  const audit: ResourcePage = await auditReply.json();
  expect(audit.items).toContainEqual(
    expect.objectContaining({
      operation: 'setManagementDebug',
      correlation_id: accepted.correlation_id,
      job_id: accepted.job_id,
      result,
    }),
  );
}

test('System Health enables and ends audited Debug under a current elevated grant', async ({
  page,
}) => {
  const account = 'browser-deadline';
  const password = fixture.accounts[account];
  expect(
    (await page.goto(fixture.origin + '/operations/health'))?.status(),
  ).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page.getByLabel('Password', { exact: true }).fill(password);
  const session = post(page, '/sessions');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await session).status()).toBe(201);

  const row = page.getByRole('row', { name: /Temporary Debug/ });
  const controls = page.getByRole('region', {
    name: 'Management service controls',
  });
  const enable = controls.getByRole('button', { name: 'Enable Debug' });
  const end = controls.getByRole('button', { name: 'End Debug' });
  const disabled = row.getByRole('cell', { name: 'disabled', exact: true });
  await expect(disabled).toBeVisible();
  await expect(enable).toBeVisible();
  await expect(enable).toBeDisabled();

  await controls.getByLabel('Confirm your password').fill(password);
  const reauthenticated = post(page, '/session/reauthentication');
  await controls.getByRole('button', { name: 'Reauthenticate' }).click();
  expect((await reauthenticated).status()).toBe(200);
  await expect(enable).toBeEnabled();

  await controls.getByLabel('Duration').selectOption('300');
  const enabled = post(page, '/health/debug');
  await enable.click();
  const enabledReply = await enabled;
  expect(enabledReply.status()).toBe(202);
  expect(enabledReply.request().postDataJSON().duration_seconds).toBe(300);
  await expect(
    row.getByRole('cell', { name: 'ready', exact: true }),
  ).toBeVisible();
  await verifyEvidence(
    page,
    await enabledReply.json(),
    'debug-lease-authorized-300s',
  );

  const ended = post(page, '/health/debug');
  await end.click();
  const endedReply = await ended;
  expect(endedReply.status()).toBe(202);
  expect(endedReply.request().postDataJSON().duration_seconds).toBe(0);
  await expect(disabled).toBeVisible();
  await expect(end).toHaveCount(0);
  await verifyEvidence(page, await endedReply.json(), 'debug-lease-disabled');
});
