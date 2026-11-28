import { expect, test } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  Job,
  SupportBundleMetadata,
  SupportCommand,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native Go/OVS frontend fixture.');
const fixture: { origin: string; accounts: Record<string, string> } =
  JSON.parse(readFileSync(fixturePath, 'utf8'));

test('Support Bundle saves only explicitly selected redacted sections after verifying the archive', async ({
  page,
}) => {
  const route = '/operations/support-bundles';
  const api = '/api/v1/support-bundles';
  const account = 'browser-bridge';
  expect((await page.goto(fixture.origin + route))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const loginReply = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await loginReply).status()).toBe(201);
  await expect(
    page.getByRole('heading', { name: 'Support Bundle', exact: true }),
  ).toBeVisible();
  const create = page.getByRole('button', {
    name: 'Create Support Bundle',
    exact: true,
  });
  await expect(create).toBeDisabled();
  await page.getByRole('checkbox', { name: 'Runtime', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Events', exact: true }).check();
  await expect(page.getByText(/Redaction is always on/)).toBeVisible();

  const requestedParts: string[] = [];
  let posts = 0;
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (request.method() === 'POST' && url.pathname === api) posts++;
    if (url.pathname.startsWith(api + '/') && url.pathname.endsWith('/parts'))
      requestedParts.push(url.searchParams.get('part') ?? '');
  });
  const reply = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' && response.url().endsWith(api),
  );
  await create.click();
  const accepted = await reply;
  expect(accepted.status()).toBe(202);
  const approval: Accepted = await accepted.json();
  const command: SupportCommand = accepted.request().postDataJSON();
  expect(command).toEqual({
    request_id: approval.request_id,
    include_kinds: ['runtime', 'events'],
    redact: true,
  });
  expect(approval.resource_ref).toEqual({
    kind: 'artifact',
    id: approval.job_id,
  });

  await expect(page).toHaveURL(
    `${fixture.origin}${route}?job_id=${approval.job_id}`,
  );
  const manifest = page.getByRole('region', { name: 'Bundle manifest' });
  await expect(manifest.getByText('Expires', { exact: true })).toBeVisible();
  const fetched = await page.request.get(
    `${fixture.origin}${api}/${approval.job_id}`,
  );
  expect(fetched.status()).toBe(200);
  const metadata: SupportBundleMetadata = await fetched.json();
  expect(metadata.manifest.sections.map((section) => section.kind)).toEqual([
    'runtime',
    'events',
  ]);
  expect(metadata.bytes).toBeGreaterThan(0);
  expect(metadata.bytes).toBeLessThanOrEqual(512 * 1024);
  expect(requestedParts).toEqual([]);

  const saving = page.waitForEvent('download');
  await manifest
    .getByRole('button', { name: 'Download verified ZIP', exact: true })
    .click();
  const download = await saving;
  expect(download.suggestedFilename()).toBe(
    `ovs-webui-support-${approval.job_id}.zip`,
  );
  const stream = await download.createReadStream();
  const contents: Buffer[] = [];
  for await (const chunk of stream) contents.push(Buffer.from(chunk));
  const archive = Buffer.concat(contents);
  expect(archive.length).toBe(metadata.bytes);
  expect(archive.subarray(0, 4)).toEqual(Buffer.from([0x50, 0x4b, 0x03, 0x04]));
  expect(createHash('sha256').update(archive).digest('hex')).toBe(
    metadata.sha256,
  );
  expect(requestedParts).toEqual(
    Array.from({ length: metadata.parts }, (_, index) => String(index)),
  );

  await page.getByRole('link', { name: 'Review Job', exact: true }).click();
  await expect(page).toHaveURL(
    `${fixture.origin}/operations/jobs/${approval.job_id}`,
  );
  const watched = await page.request.get(
    `${fixture.origin}/api/v1/jobs/${approval.job_id}`,
  );
  expect(watched.status()).toBe(200);
  const job: Job = await watched.json();
  expect(job).toMatchObject({
    id: approval.job_id,
    operation: 'createSupportBundle',
    state: 'succeeded',
  });
  await expect(
    page.getByRole('link', { name: 'Review Support Bundle', exact: true }),
  ).toBeVisible();
  expect(posts).toBe(1);
});
