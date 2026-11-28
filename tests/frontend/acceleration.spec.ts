import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type { RuntimeAccelerationEvidence } from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires the isolated native Go/OVS frontend fixture.');
const fixture: { origin: string; accounts: Record<string, string> } = JSON.parse(readFileSync(fixturePath, 'utf8'));

async function login(page: Page, account: string, path: string) {
  expect((await page.goto(fixture.origin + path))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page.getByLabel('Password', { exact: true }).fill(fixture.accounts[account]);
  const reply = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().endsWith('/api/v1/sessions'));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
}

test('DPDK and Offload keep native OVS configuration, reported runtime and Linux host evidence separate', async ({ page }) => {
  await login(page, 'browser-native-types', '/visibility/dpdk');
  const writes: string[] = [];
  page.on('request', (request) => {
    if (request.url().endsWith('/api/v1/runtime/acceleration') && request.method() !== 'GET') writes.push(request.method());
  });
  const result = await page.request.get(fixture.origin + '/api/v1/runtime/acceleration');
  expect(result.status()).toBe(200);
  const evidence: RuntimeAccelerationEvidence = await result.json();
  expect(evidence.ovs.state).toBe('ready');
  await expect(page.getByRole('heading', { name: 'DPDK', exact: true })).toBeVisible();
  const configuration = page.getByRole('region', { name: 'OVSDB configuration', exact: true });
  const runtime = page.getByRole('region', { name: 'OVS-reported runtime', exact: true });
  const host = page.getByRole('region', { name: 'Linux global memory and NUMA', exact: true });
  await expect(configuration.getByText('DPDK initialization setting', { exact: true })).toBeVisible();
  await expect(runtime.getByText('Runtime PMD threads', { exact: true })).toBeVisible();
  const pmd = runtime.getByText('Runtime PMD threads', { exact: true }).locator('xpath=following-sibling::dd[1]');
  await expect(pmd).toHaveText('unknown');
  await expect(host).toBeVisible();
  await expect(configuration.getByText('ovsdb-root-configuration', { exact: true })).toHaveCount(0);

  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(configuration.getByText('ovsdb-root-configuration', { exact: true })).toBeVisible();
  await expect(runtime.getByText('ovsdb-reported-runtime', { exact: true })).toBeVisible();
  await expect(pmd.getByText('DPDK_PMD_RUNTIME_UNOBSERVED', { exact: true })).toBeVisible();
  if (evidence.host?.state === 'observed' || evidence.host?.state === 'partial') {
    await expect(host.getByText('linux-global-sysfs', { exact: true })).toBeVisible();
    await expect(host.getByText('Online NUMA nodes', { exact: true })).toBeVisible();
    for (const pool of evidence.host.huge_pages) {
      const size = pool.size_kib === 2048 ? '2 MiB' : pool.size_kib === 1048576 ? '1 GiB' : `${pool.size_kib} KiB`;
      await expect(host.getByRole('heading', { name: `${size} huge pages`, exact: true })).toBeVisible();
    }
  }

  const nav = page.getByRole('navigation', { name: 'Visibility views', exact: true });
  await nav.getByRole('link', { name: 'Hardware Offload', exact: true }).click();
  await expect(page).toHaveURL(fixture.origin + '/visibility/offload');
  expect((await page.reload())?.status()).toBe(200);
  await expect(configuration.getByText('Configured hardware offload', { exact: true })).toBeVisible();
  const hardware = runtime.getByText('Runtime hardware flows', { exact: true }).locator('xpath=following-sibling::dd[1]');
  await expect(hardware.getByText('unknown', { exact: true })).toBeVisible();
  await expect(hardware.getByText('HARDWARE_OFFLOAD_RUNTIME_UNOBSERVED', { exact: true })).toBeVisible();
  expect(writes).toEqual([]);
});

test('Acceleration routes require inventory permission alongside state permission', async ({ page }) => {
  await login(page, 'browser-tree-denied', '/visibility/offload');
  await expect(page.getByRole('heading', { name: 'Hardware Offload', exact: true })).toBeVisible();
  await expect(page.getByText('Permission denied', { exact: true })).toBeVisible();
  const nav = page.getByRole('navigation', { name: 'Visibility views', exact: true });
  await expect(nav.getByRole('link', { name: 'Hardware Offload', exact: true })).toHaveCount(0);
  await expect(nav.getByRole('link', { name: 'DPDK', exact: true })).toHaveCount(0);
  expect((await page.request.get(fixture.origin + '/api/v1/runtime/acceleration')).status()).toBe(403);
});
