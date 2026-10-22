import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';

if (!process.env.OVS_SPANNING_TREE_FRONTEND_FIXTURE)
  throw new Error('Requires the qualified native OVS / production service fixture.');
const fixture = JSON.parse(readFileSync(process.env.OVS_SPANNING_TREE_FRONTEND_FIXTURE, 'utf8'));
const source = `/bridges/${fixture.bridge.management_id}/spanning-tree`;
const href = fixture.origin + `/switching/spanning-tree/${fixture.bridge.management_id}`;
const output = 'test-results/ovs-witness/frontend-evidence';
const native = () => execFileSync(fixture.nativeBinary,
  ['--timeout=5', `--db=unix:${fixture.dbSocket}`, 'get', 'Bridge', 'br-native-tree', 'stp_enable', 'rstp_enable', 'other_config'],
  { encoding: 'utf8' }).trim();
async function get(context: BrowserContext, path: string) {
  const r = await context.request.get(fixture.origin + '/api/v1' + path);
  expect(r.status(), path).toBe(200);
  return r.json();
}
async function screen(page: Page, name: string) {
  mkdirSync(output, { recursive: true });
  await page.screenshot({ path: `${output}/${name}.png`, fullPage: true });
}
async function login(page: Page, name: string) {
  await page.goto(href);
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page.getByLabel('Password', { exact: true }).fill(fixture.accounts[name]);
  const reply = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/v1/sessions'));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
  await page.goto(href);
  await expect(page.getByRole('region', { name: 'Spanning tree change preparation', exact: true })).toBeVisible();
}
async function stage(page: Page, protocol: 'stp'|'rstp', priority: string) {
  await page.getByRole('region', { name: 'Spanning tree change preparation', exact: true }).locator('summary').click();
  await page.getByLabel('Spanning tree protocol', { exact: true }).selectOption(protocol);
  await page.getByLabel(protocol.toUpperCase() + ' priority', { exact: true }).fill(priority);
  await page.keyboard.press('Tab');
  const nextField=protocol==='stp'?'STP hello time (seconds)':'RSTP max age (seconds)';
  await expect(page.getByLabel(nextField, { exact: true })).toBeFocused();
  await screen(page, 'native-tree-editor-' + protocol);
  await page.getByRole('button', { name: 'Stage spanning tree change', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Candidate Workspace', exact: true })).toBeVisible();
  const c = await get(page.context(), '/candidate');
  expect(c.intents[0].operation).toBe('bridge.spanning-tree.configure');
  expect(c.diff).toHaveLength(9);
  await page.getByRole('button', { name: 'Validate Candidate', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Diff & Validation', exact: true })).toBeVisible();
  await expect(page.getByText('Usable for this revision', { exact: true })).toBeVisible();
  await page.getByLabel('Change reason', { exact: true }).fill('Synthetic native spanning-tree qualification');
  await page.getByRole('checkbox').check();
  await expect(page.getByRole('button', { name: 'Start Safe Apply', exact: true })).toBeEnabled();
}
async function apply(page: Page) {
  await page.getByRole('button', { name: 'Start Safe Apply', exact: true }).click();
  await expect(page).toHaveURL(/\/changes\/transactions\/[0-9a-f-]+$/);
  const path = new URL(page.url()).pathname.replace('/changes', '');
  await expect.poll(async () => (await get(page.context(), path)).safe_apply).toBe('awaiting-confirmation');
  const transaction = await get(page.context(), path);
  expect(transaction.applied_outcome).toBe('applied');
  expect(transaction.applied_evidence.next_cfg_target).not.toBeNull();
  await expect(page.getByTestId('safe-state')).toHaveText('awaiting-confirmation');
  await expect(page.getByRole('button', { name: 'Confirm configuration', exact: true })).toBeEnabled();
  return path;
}

test('default hardened service refuses unproven native source without changing OVS', async ({ page }) => {
  await login(page, 'native-tree-denied');
  const before = native();
  await stage(page, 'rstp', '4096');
  const reply = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/v1/transactions'));
  await page.getByRole('button', { name: 'Start Safe Apply', exact: true }).click();
  const denied = await reply;
  expect(denied.status()).toBe(409);
  expect((await denied.json()).code).toBe('SPANNING_TREE_EXECUTION_EVIDENCE_UNPROVEN');
  expect(native()).toBe(before);
  await screen(page, 'native-source-restricted');
});

test('qualified native execution reviews responsive UI and confirms actual installed RSTP', async ({ page }) => {
  // Explicit test-only profile transition. The shipped default unit is never
  // weakened and there is no automatic privilege or source fallback in mgrd.
  expect(fixture.unit).toMatch(/^ovs-mgrd-inventory-[0-9a-f]+\.service$/);
  expect(fixture.unitPath).toBe('/etc/systemd/system/' + fixture.unit);
  const unit = readFileSync(fixture.unitPath, 'utf8');
  expect(unit).toContain('CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n');
  execFileSync('systemctl', ['is-active', '--quiet', fixture.unit]);
  execFileSync('systemctl', ['stop', fixture.unit]);
  writeFileSync(fixture.unitPath, unit.replace('CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n',
    'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW CAP_SYS_PTRACE\n'));
  execFileSync('systemctl', ['daemon-reload']);
  execFileSync('systemctl', ['reset-failed', fixture.unit]);
  execFileSync('systemctl', ['start', fixture.unit]);
  await login(page, 'native-tree-confirm');
  await expect.poll(async () => (await get(page.context(), source)).execution.available).toBe(true);
  await screen(page, 'native-tree-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await screen(page, 'native-tree-expert');
  await page.getByRole('button', { name: 'Toggle color theme', exact: true }).click();
  await screen(page, 'native-tree-dark');
  await page.getByRole('button', { name: 'Toggle color theme', exact: true }).click();
  for (const [width, height, label] of [[900,1100,'tablet'],[390,844,'mobile']] as const) {
    await page.setViewportSize({ width, height });
    await expect(page.getByRole('button', { name: 'Stage spanning tree change', exact: true })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await screen(page, 'native-tree-' + label);
  }
  await page.setViewportSize({ width:1440, height:1000 });
  await stage(page, 'rstp', '4096');
  await screen(page, 'native-tree-validation');
  const path = await apply(page);
  await screen(page, 'native-tree-awaiting-confirmation');
  await expect(page.getByRole('button', { name: 'Confirm configuration', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Confirm configuration', exact: true }).click();
  await expect.poll(async () => (await get(page.context(), path)).safe_apply).toBe('confirmed');
  await expect(page.getByTestId('safe-state')).toHaveText('confirmed');
  const row = await get(page.context(), source);
  expect(row.spanning_tree.protocol).toBe('rstp');
  expect(row.spanning_tree.configuration['rstp-priority'].value).toBe('4096');
  await screen(page, 'native-tree-confirmed');
});

test('actual management path loss restores captured native flags and raw parameters without replay', async ({ page }) => {
  await login(page, 'native-tree-recover');
  const original = native();
  await stage(page, 'stp', '8192');
  const path = await apply(page);
  try {
    execFileSync('ip', ['link', 'set', fixture.probeInterface, 'down']);
    await expect.poll(() => native()).toBe(original);
  } finally {
    execFileSync('ip', ['link', 'set', fixture.probeInterface, 'up']);
  }
  await expect.poll(async () => (await get(page.context(), path)).safe_apply).toBe('rolled-back');
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  const counter = () => execFileSync(fixture.nativeBinary,
    ['--timeout=5',`--db=unix:${fixture.dbSocket}`,'get','Open_vSwitch','.','next_cfg'],{encoding:'utf8'}).trim();
  const settled = counter();
  await page.getByRole('button', { name: 'Refresh evidence', exact: true }).click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  expect(native()).toBe(original);
  expect(counter()).toBe(settled);
  await screen(page, 'native-tree-management-recovered');
});
