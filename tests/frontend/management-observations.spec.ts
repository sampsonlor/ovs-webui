import { expect, test, type Page } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import type {
  Resource,
  ResourcePage,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: { origin: string; accounts: Record<string, string> } =
  JSON.parse(readFileSync(fixturePath, 'utf8'));
const health = '/operations/health';
const capabilities = '/admin/capabilities';

async function login(page: Page, account: string, path: string) {
  expect((await page.goto(fixture.origin + path))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const reply = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
}

async function screenshot(page: Page, file: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: 'test-results/frontend-evidence/management-' + file + '.png',
    fullPage: true,
  });
}

test('Management pages render live readiness and read-only capability evidence', async ({
  page,
}) => {
  await login(page, 'browser-interfaces', health);
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() !== 'GET' &&
      /\/api\/v1\/(health|capabilities)(?:[/?]|$)/.test(request.url())
    )
      writes.push(request.method());
  });

  await expect(
    page.getByRole('heading', { name: 'System Health', exact: true }),
  ).toBeVisible();
  const response = await page.request.get(fixture.origin + '/api/v1/health');
  expect(response.status()).toBe(200);
  const observed: Resource = await response.json();
  expect(observed.allowed_actions).toEqual([]);
  expect(observed.components?.map((component) => component.key)).toEqual(
    expect.arrayContaining([
      'webd',
      'web-db',
      'manager',
      'manager-db',
      'ovsdb',
    ]),
  );
  const provider = observed.components?.find(
    (component) => component.key === 'ovsdb',
  );
  if (!provider) throw new Error('Missing current OVSDB health evidence');
  const components = page.getByRole('region', {
    name: 'Management components',
    exact: true,
  });
  const ovsdb = components.getByRole('row', { name: /OVSDB provider/ });
  await expect(ovsdb).toBeVisible();
  await expect(
    components.getByRole('columnheader', { name: 'Source', exact: true }),
  ).toHaveCount(0);
  const evidence = page.getByRole('navigation', {
    name: 'Related operational evidence',
  });
  await expect(evidence.getByRole('link', { name: 'Events' })).toHaveAttribute(
    'href',
    '/operations/events',
  );
  await screenshot(page, 'health-standard');

  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    components.getByRole('columnheader', { name: 'Source', exact: true }),
  ).toBeVisible();
  await expect(
    ovsdb.getByText(provider.source.authority, { exact: true }),
  ).toBeVisible();
  await screenshot(page, 'health-expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();

  await evidence
    .getByRole('link', { name: 'Capabilities', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Capabilities', exact: true }),
  ).toBeVisible();
  const list = await page.request.get(fixture.origin + '/api/v1/capabilities');
  expect(list.status()).toBe(200);
  const registry: ResourcePage = await list.json();
  const openflow = page.getByRole('row', { name: /OpenFlow observation/ });
  await expect(
    openflow.getByText('unavailable', { exact: true }),
  ).toBeVisible();
  const entry = registry.items.find(
    (item) => item.capability_evidence?.required_capabilities.length,
  );
  if (!entry?.capability_evidence)
    throw new Error('Missing capability permission evidence');
  expect(entry.allowed_actions).toEqual([]);
  const capabilityLink = page
    .getByRole('region', { name: 'Capability registry', exact: true })
    .getByRole('link', { name: entry.capability_evidence.label, exact: true });
  const capabilityPath = capabilities + '/' + entry.id;
  await expect(capabilityLink).toHaveAttribute('href', capabilityPath);
  await capabilityLink.focus();
  await expect(capabilityLink).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(fixture.origin + capabilityPath);
  expect((await page.reload())?.status()).toBe(200);
  await expect(
    page.getByRole('heading', {
      name: entry.capability_evidence.label,
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText('Current grant', { exact: true })).toBeVisible();
  await expect(page.getByText('Recovery', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Capability evidence', exact: true }),
  ).toHaveCount(0);
  await screenshot(page, 'capability-standard');

  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  const native = page.getByRole('region', {
    name: 'Capability evidence',
    exact: true,
  });
  await expect(
    native.getByText(entry.capability_evidence.key, { exact: true }),
  ).toBeVisible();
  for (const permission of entry.capability_evidence.required_capabilities)
    await expect(native.getByText(permission, { exact: true })).toBeVisible();
  await screenshot(page, 'capability-expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(391);
  await expect(
    page.getByRole('heading', {
      name: entry.capability_evidence.label,
      exact: true,
    }),
  ).toBeVisible();
  await screenshot(page, 'capability-mobile');
  await expect(
    page
      .getByRole('main')
      .getByRole('button', { name: /restart|enable|apply|confirm/i }),
  ).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('Health access does not grant Capabilities access in Standard or Expert', async ({
  page,
}) => {
  await login(page, 'browser-interface-observer', health);
  await expect(
    page.getByRole('region', { name: 'Management components', exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole('navigation', { name: 'Management views' })
      .getByRole('link', { name: 'Capabilities', exact: true }),
  ).toHaveCount(0);
  expect(
    (await page.request.get(fixture.origin + '/api/v1/capabilities')).status(),
  ).toBe(403);
  expect((await page.goto(fixture.origin + capabilities))?.status()).toBe(200);
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Capability registry', exact: true }),
  ).toHaveCount(0);
  await screenshot(page, 'capabilities-withheld');
});
