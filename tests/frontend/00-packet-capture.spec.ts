import { expect, test } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  CaptureArtifact,
  CaptureCommand,
  CapturePart,
  CaptureTargets,
  Job,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native Go/OVS frontend fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  captureTarget: { id: string; name: string };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

test('Packet Capture starts only a reviewed native Interface Job and verifies its bounded PCAP download', async ({
  page,
}) => {
  const account = 'browser-capture';
  const capture = `${fixture.origin}/api/v1/captures`;
  const profile = 'arp-ethernet-headers';
  const maximumBytes = 54;
  const targetRef = { kind: 'interface', id: fixture.captureTarget.id };
  expect(
    (await page.goto(`${fixture.origin}/operations/packet-capture`))?.status(),
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
    page.getByRole('heading', { name: 'Packet Capture', exact: true }),
  ).toBeVisible();

  let posted = 0;
  page.on('request', (request) => {
    if (request.url() === capture && request.method() === 'POST') posted++;
  });
  const reply = await page.request.get(`${capture}/targets`);
  expect(reply.status()).toBe(200);
  const policy: CaptureTargets = await reply.json();
  expect(policy).toEqual({
    policy_state: 'enabled',
    targets: [fixture.captureTarget],
  });
  const start = page.getByRole('button', {
    name: 'Start Packet Capture',
    exact: true,
  });
  const target = page.getByRole('combobox', {
    name: 'Approved Interface',
    exact: true,
  });
  await expect(target.getByRole('option')).toHaveCount(2);
  await expect(
    target.getByRole('option', {
      name: fixture.captureTarget.name,
      exact: true,
    }),
  ).toHaveAttribute('value', fixture.captureTarget.id);
  const desktop = page.viewportSize()!;
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByText('Use a desktop to review and start a new Packet Capture.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(start).toHaveCount(0);
  await expect(
    page.getByRole('link', { name: 'Review Jobs', exact: true }),
  ).toBeVisible();
  expect(posted).toBe(0);
  await page.setViewportSize(desktop);

  await target.selectOption(fixture.captureTarget.id);
  const duration = page.getByLabel('Duration in seconds', { exact: true });
  const bytes = page.getByLabel('Maximum bytes', { exact: true });
  await expect(duration).toHaveValue('30');
  await expect(bytes).toHaveValue('131072');
  await duration.fill('1');
  await bytes.fill(String(maximumBytes));
  await expect(page.getByLabel('Capture profile', { exact: true })).toHaveValue(
    profile,
  );
  const review = page.getByRole('checkbox', {
    name: 'I reviewed the selected Interface, profile and limits and accept the capture load.',
    exact: true,
  });
  await review.check();
  await expect(start).toBeDisabled();
  await review.uncheck();
  await page
    .getByLabel('Confirm your password', { exact: true })
    .fill(fixture.accounts[account]);
  const elevated = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/v1/session/reauthentication'),
  );
  await page
    .getByRole('button', { name: 'Reauthenticate', exact: true })
    .click();
  expect((await elevated).status()).toBe(200);
  await expect(
    page.getByRole('button', { name: 'Reauthenticate', exact: true }),
  ).toHaveCount(0);
  await expect(start).toBeDisabled();
  await review.check();
  const create = page.waitForResponse(
    (response) =>
      response.url() === capture && response.request().method() === 'POST',
  );
  await start.click();
  const accepted = await create;
  expect(accepted.status()).toBe(202);
  const command: CaptureCommand = accepted.request().postDataJSON();
  const receipt: Accepted = await accepted.json();
  expect(command).toEqual({
    request_id: receipt.request_id,
    interface_id: targetRef.id,
    duration_seconds: 1,
    max_bytes: maximumBytes,
    filter: profile,
  });
  expect(receipt.resource_ref).toEqual(targetRef);
  const jobID = receipt.job_id;
  await expect(page).toHaveURL(`${fixture.origin}/operations/jobs/${jobID}`);
  const result = page.getByRole('region', {
    name: 'Packet Capture result',
    exact: true,
  });
  const downloadButton = result.getByRole('button', {
    name: 'Download verified PCAP',
    exact: true,
  });
  await expect(downloadButton).toBeEnabled();

  const jobResponse = await page.request.get(
    `${fixture.origin}/api/v1/jobs/${jobID}`,
  );
  const artifactResponse = await page.request.get(`${capture}/${jobID}`);
  expect(jobResponse.status()).toBe(200);
  expect(artifactResponse.status()).toBe(200);
  const job: Job = await jobResponse.json();
  expect(job).toMatchObject({
    id: jobID,
    operation: 'createCapture',
    resource_ref: targetRef,
    state: 'succeeded',
    business_outcome: 'success',
    correlation_id: receipt.correlation_id,
  });
  const artifact: CaptureArtifact = await artifactResponse.json();
  // This fixture has no IP traffic on either veth endpoint. The independent
  // native namespace test covers matching ARP frames and payload truncation.
  expect(artifact).toMatchObject({
    id: jobID,
    target_ref: targetRef,
    profile,
    state: 'no-data',
    packets: 0,
    bytes: 24,
    parts: 1,
  });
  await expect(
    result.getByText('No matching packets were observed.', { exact: true }),
  ).toBeVisible();
  await expect(
    result.getByRole('link', { name: 'Review Interface', exact: true }),
  ).toHaveAttribute('href', `/interfaces/${targetRef.id}`);

  const [partReply, download] = await Promise.all([
    page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        response.url() === `${capture}/${jobID}/parts?part=0`,
    ),
    page.waitForEvent('download', { timeout: 15_000 }),
    downloadButton.click(),
  ]);
  expect(partReply.status()).toBe(200);
  const part: CapturePart = await partReply.json();
  expect(part).toMatchObject({ id: jobID, part: 0 });
  expect(download.suggestedFilename()).toBe(`ovs-webui-capture-${jobID}.pcap`);
  const pcap = readFileSync((await download.path())!);
  expect(pcap.length).toBe(artifact.bytes);
  expect(pcap.toString('base64')).toBe(part.data_base64);
  expect(createHash('sha256').update(pcap).digest('hex')).toBe(artifact.sha256);
  expect(pcap.readUInt32LE(0)).toBe(0xa1b2c3d4);
  expect(pcap.readUInt32LE(16)).toBe(14);
  expect(pcap.readUInt32LE(20)).toBe(1);
  expect(posted).toBe(1);
});
