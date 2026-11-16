import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import type { Resource } from '../../clients/typescript/public-v1.generated';

const path = process.env.OVS_FRONTEND_FIXTURE;
if (!path) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  ovsDirectory: string;
  accounts: Record<string, string>;
  qosEdit: {
    id: string;
    port: string;
    bridge: string;
    uuid: string;
    queue: string;
  };
} = JSON.parse(readFileSync(path, 'utf8'));
const href = fixture.origin + '/switching/qos';
const vsctl = (...args: string[]) =>
  execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    {
      encoding: 'utf8',
    },
  ).trim();

async function login(page: Page, user: string) {
  expect((await page.goto(href))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(user);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[user]);
  const response = page.waitForResponse(
    (reply) =>
      reply.request().method() === 'POST' &&
      reply.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await response).status()).toBe(201);
  await page.goto(href);
}

test('Authorized QoS rate editing stages, validates, applies and restores the installed rates', async ({
  page,
  browser,
}) => {
  const detail = href + '/' + fixture.qosEdit.id;
  const read = async () => {
    const response = await page.request.get(
      fixture.origin + '/api/v1/qos/' + fixture.qosEdit.id,
    );
    expect(response.status()).toBe(200);
    return (await response.json()) as Resource;
  };
  const seeRate = async (maximum: string) => {
    await expect
      .poll(async () => {
        const qos = await read();
        return [
          qos.native_qos?.max_rate.bps,
          qos.linux_qos?.installed_rates?.maximum_bps,
        ];
      })
      .toEqual([maximum, maximum]);
  };
  const restricted = await browser.newPage({ ignoreHTTPSErrors: true });
  try {
    await login(restricted, 'browser-qos-runtime');
    await restricted.goto(detail);
    await expect(
      restricted.getByRole('heading', { name: 'Native configuration' }),
    ).toBeVisible();
    await expect(
      restricted.getByRole('button', { name: 'Edit QoS rates' }),
    ).toHaveCount(0);
  } finally {
    await restricted.close();
  }
  await login(page, 'browser-qos-edit');
  await page.goto(detail);
  await seeRate('8000000');
  const edit = page.getByRole('button', {
    name: 'Edit QoS rates',
    exact: true,
  });
  await page.setViewportSize({ width: 820, height: 960 });
  await expect(edit).toHaveCount(0);
  await expect(
    page.getByText('Use a desktop to stage QoS rate changes.'),
  ).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await edit.click();
  await page
    .getByLabel('QoS maximum rate (bit/s)', { exact: true })
    .fill('12000000');
  await page.getByLabel('Queue 7 minimum rate (bit/s)').fill('2000000');
  const maximum = page.getByLabel('Queue 7 maximum rate (bit/s)');
  await maximum.fill('16000000');
  const stage = page.getByRole('button', {
    name: 'Stage in Candidate',
    exact: true,
  });
  await stage.click();
  await expect(page.getByRole('alert')).toContainText('Each Queue minimum');
  await maximum.fill('4000000');
  await expect(
    page.getByRole('alert').filter({ hasText: 'Each Queue minimum' }),
  ).toHaveCount(0);
  const observedAt = page
    .getByText('Observed at', { exact: true })
    .locator('xpath=following-sibling::dd[1]');
  const initialObservation = await observedAt.textContent();
  await expect(observedAt).not.toHaveText(initialObservation ?? '');
  await expect(maximum).toHaveValue('4000000');
  await page.screenshot({
    path: 'test-results/frontend-evidence/qos-rate-editor.png',
    fullPage: true,
  });
  await stage.click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace' }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  await expect(
    diff
      .getByRole('row', { name: /Queue 7 maximum rate/ })
      .getByRole('cell')
      .last(),
  ).toHaveText('4000000');
  await seeRate('8000000');
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByText('Usable for this revision', { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Verify your password')
    .fill(fixture.accounts['browser-qos-edit']);
  await page
    .getByRole('button', { name: 'Verify identity', exact: true })
    .click();
  await page
    .getByLabel('Change reason')
    .fill('Change rates on an isolated QoS test port');
  await page.getByRole('checkbox', { name: /^I reviewed this Diff/ }).check();
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
  await seeRate('12000000');
  await page.screenshot({
    path: 'test-results/frontend-evidence/qos-rate-applied.png',
    fullPage: true,
  });
  await page
    .getByRole('button', { name: 'Request rollback', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  await seeRate('8000000');
  expect(
    vsctl('get', 'Queue', fixture.qosEdit.queue, 'other_config:fixture-opaque'),
  ).toBe('keep');
  await page.screenshot({
    path: 'test-results/frontend-evidence/qos-rate-rolled-back.png',
    fullPage: true,
  });

  // Return the shared topology to its original size before its browser cases.
  vsctl('--if-exists', 'del-br', fixture.qosEdit.bridge);
  vsctl('--if-exists', 'destroy', 'QoS', fixture.qosEdit.uuid);
  vsctl('--if-exists', 'destroy', 'Queue', fixture.qosEdit.queue);
  await expect
    .poll(async () => {
      const response = await page.request.get(
        fixture.origin + '/api/v1/ports?filter=' +
          encodeURIComponent(fixture.qosEdit.port),
      );
      expect(response.status()).toBe(200);
      return (await response.json()).items.length;
    })
    .toBe(0);
});
