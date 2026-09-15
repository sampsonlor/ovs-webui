import type { Page, TestInfo } from '@playwright/test';
import type { ControlState } from '../../lib/change-control';
import { test, expect } from './fixtures';

test.use({ serverMode: 'prototype' });
type Tool = { name: string; execute: (input: unknown) => unknown };
type TestWindow = Window & { inventoryTools: Map<string, Tool> };
type State = ControlState & {
  inventory: null | { ports: number; interfaces: number };
};

async function call<T = unknown>(
  page: Page,
  name: string,
  input: unknown = {},
): Promise<T> {
  return page.evaluate(
    async ({ toolName, args }) => {
      const tool = (window as unknown as TestWindow).inventoryTools.get(
        toolName,
      );
      if (!tool) throw new Error(`Missing registered tool: ${toolName}`);
      return await tool.execute(args);
    },
    { toolName: name, args: input },
  ) as Promise<T>;
}
const read = (page: Page) => call<State>(page, 'read_prototype_state');
async function open(page: Page) {
  await page.addInitScript(() => {
    const registered = new Map<string, Tool>();
    (window as unknown as TestWindow).inventoryTools = registered;
    Object.defineProperty(document, 'modelContext', {
      configurable: true,
      value: {
        registerTool(tool: Tool, options: { signal: AbortSignal }) {
          registered.set(tool.name, tool);
          options.signal.addEventListener('abort', () =>
            registered.delete(tool.name),
          );
        },
      },
    });
  });
  await page.goto('/');
  await expect(
    page.getByRole('button', {
      name: 'Navigation capability states',
      exact: true,
    }),
  ).toContainText('Available · 1');
}
async function capture(page: Page, info: TestInfo, name: string) {
  if (process.env.OVS_BROWSER_CAPTURE === '1') {
    const dismiss = page.getByRole('button', { name: 'Dismiss notification' });
    if (await dismiss.isVisible()) await dismiss.click();
    await page.screenshot({
      path: info.outputPath(`${name}.png`),
      fullPage: true,
    });
  }
}
const heading = (page: Page, name: string) =>
  page.getByRole('heading', { name, exact: true });

test('Port, Bridge, Bond and Interface links retain the same members through history and reload', async ({
  page,
}, info) => {
  await open(page);
  await expect(
    page.getByLabel('Inventory summary', { exact: true }),
  ).toContainText('10 ports');
  await page
    .getByRole('button', { name: 'Inspect bond-storage', exact: true })
    .click();
  await expect(heading(page, 'bond-storage')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'enp130s0f1', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: /^Bond \/ LACP/ }),
  ).toContainText('Manage');
  const portUrl = page.url();
  expect(portUrl).toContain('facet=port');
  const skip = page.getByRole('link', {
    name: 'Skip to page content',
    exact: true,
  });
  await skip.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#page-content')).toBeFocused();
  expect(page.url()).toBe(portUrl);
  const titleBox = await heading(page, 'bond-storage').boundingBox();
  const headerBox = await page.locator('header').boundingBox();
  expect(titleBox!.y).toBeGreaterThanOrEqual(headerBox!.y + headerBox!.height);
  await capture(page, info, 'shared-port');
  await page.getByRole('button', { name: 'br-storage', exact: true }).click();
  await expect(heading(page, 'br-storage')).toBeVisible();
  const storageRow = page.getByRole('row').filter({
    has: page.getByRole('button', { name: 'bond-storage', exact: true }),
  });
  await expect(storageRow).toContainText('enp130s0f0');
  await expect(storageRow).toContainText('enp130s0f1');
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await capture(page, info, 'shared-bridge-expert');
  await page.getByRole('button', { name: 'bond-storage', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Edit bond intent', exact: true }),
  ).toBeVisible();
  expect(page.url()).toContain('facet=bond');
  const member = page.getByRole('button', { name: 'enp130s0f1', exact: true });
  await member.focus();
  await page.keyboard.press('Enter');
  await expect(heading(page, 'enp130s0f1')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Open Port bond-storage', exact: true }),
  ).toBeVisible();
  const interfaceUrl = page.url();
  expect(interfaceUrl).toContain('/Interface/');
  await capture(page, info, 'interface-expert');
  await page.goBack();
  await expect(heading(page, 'bond-storage')).toBeVisible();
  await page.goForward();
  await expect(heading(page, 'enp130s0f1')).toBeVisible();
  await page.reload();
  await expect(heading(page, 'enp130s0f1')).toBeVisible();
  expect(page.url()).toBe(interfaceUrl);
  await page
    .getByRole('button', { name: 'Open Bridge br-storage', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'storage-node-01', exact: true })
    .click();
  await expect(heading(page, 'storage-node-01')).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'enp130s0f2', exact: true }),
  ).toBeVisible();
  expect((await read(page)).candidate).toBeNull();
});

test('foreign, old-generation, missing and malformed URLs never display a default object', async ({
  page,
}, info) => {
  await open(page);
  await call(page, 'open_inventory_object', { target: 'Port/bond-storage' });
  const known = new URL(page.url()).hash;
  const paths = [
    known.replace('ovs-synthetic-01', 'another-instance'),
    known.replace('generation=1842', 'generation=1841'),
    known.replace(
      '83cb0000-0000-4000-8000-0000000041c9',
      'ffffffff-ffff-4fff-8fff-ffffffffffff',
    ),
    '#object/%ZZ/Port/id?generation=1842',
    '#object/ovs-synthetic-01/Port/id?generation=NaN',
    '#view/bond-detail',
  ];
  for (const hash of paths) {
    await page.goto(`/${hash}`);
    await expect(heading(page, 'Object unavailable')).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Edit bond intent', exact: true }),
    ).toHaveCount(0);
    await expect(heading(page, 'bond-uplink')).toHaveCount(0);
    await expect(heading(page, 'server-07')).toHaveCount(0);
  }
  await capture(page, info, 'unavailable-reference');
  await page
    .getByRole('button', { name: 'Open switching inventory', exact: true })
    .click();
  await expect(heading(page, 'Switching overview')).toBeVisible();
});

test('unobserved Bond targets and object history cannot replace or unlock an unknown transaction', async ({
  page,
}, info) => {
  await open(page);
  await call(page, 'stage_bond_change', {
    name: 'bond-not-observed',
    bridge: 'br-fabric',
    mode: 'balance-tcp',
    lacp: 'active',
  });
  await call(page, 'navigate_prototype_view', { view: 'bond-detail' });
  await expect(heading(page, 'Object unavailable')).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Unavailable object reference' }),
  ).toContainText('Port/bond-not-observed');
  await call(page, 'validate_workspace');
  await call(page, 'start_safe_apply', {
    reason: 'Review object identity while recovery is pending',
  });
  await call(page, 'set_review_scenario', { scenario: 'outcome-unknown' });
  const before = await read(page);
  await call(page, 'open_inventory_object', { target: 'Interface/enp130s0f1' });
  await expect(heading(page, 'enp130s0f1')).toBeVisible();
  await page
    .getByRole('button', { name: 'Open Bridge br-storage', exact: true })
    .click();
  await page.goBack();
  await expect(heading(page, 'enp130s0f1')).toBeVisible();
  await call(page, 'navigate_prototype_view', { view: 'evidence' });
  await page
    .getByRole('button', { name: 'Port/bond-not-observed', exact: true })
    .first()
    .click();
  await expect(heading(page, 'Object unavailable')).toBeVisible();
  const after = await read(page);
  expect(after.candidate).toEqual(before.candidate);
  expect(after.transaction).toEqual(before.transaction);
  expect(after.live).toEqual(before.live);
  await expect(
    call(page, 'complete_safe_apply', { decision: 'confirm' }),
  ).rejects.toThrow();
  await capture(page, info, 'unknown-original-target');
});

test('Interface context preserves Unknown, permission and provider states with no configuration on narrow devices', async ({
  page,
}, info) => {
  await open(page);
  await call(page, 'open_inventory_object', { target: 'Interface/pf0hpf' });
  await expect(heading(page, 'pf0hpf')).toBeVisible();
  const context = page.getByRole('region', {
    name: 'Interface relationship context',
  });
  await expect(context.getByText('Unknown', { exact: true })).toBeVisible();
  for (const mode of ['expert', 'standard']) {
    await page.getByRole('button', { name: mode, exact: true }).click();
    await expect(
      context.getByRole('button', { name: /Edit|Apply|Enable/ }),
    ).toHaveCount(0);
  }
  await call(page, 'set_review_scenario', { scenario: 'provider-degraded' });
  await expect(
    page.getByText('Retained relationship snapshot', { exact: true }),
  ).toBeVisible();
  await call(page, 'set_review_scenario', { scenario: 'provider-unavailable' });
  await expect(
    page.getByText('Object observations unavailable', { exact: true }),
  ).toBeVisible();
  await expect(context).toHaveCount(0);
  await call(page, 'set_review_scenario', { scenario: 'normal' });
  await page.evaluate(() => document.documentElement.classList.add('dark'));
  await capture(page, info, 'interface-dark');
  await page.evaluate(() => {
    document.documentElement.classList.remove('dark');
    document.documentElement.style.fontSize = '200%';
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await capture(page, info, 'interface-enlarged');
  await page.evaluate(() => {
    document.documentElement.style.fontSize = '';
  });
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(heading(page, 'pf0hpf')).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await capture(page, info, `interface-${width}`);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await call(page, 'set_review_scenario', { scenario: 'permission-denied' });
  await expect(context).toHaveCount(0);
  const denied = await call<{ status: string; object?: unknown }>(
    page,
    'open_inventory_object',
    { target: 'Port/bond-storage' },
  );
  expect(denied.status).toBe('unavailable');
  expect(denied.object).toBeUndefined();
  expect((await read(page)).inventory).toBeNull();
  expect((await read(page)).candidate).toBeNull();
});

test('confirmed VLAN configuration agrees between the Port and its Bridge child row', async ({
  page,
}) => {
  await open(page);
  await call(page, 'stage_vlan_change', {
    port: 'server-07',
    vlanMode: 'trunk',
    allowedVlans: '120, 240',
  });
  await call(page, 'validate_workspace');
  await call(page, 'start_safe_apply', {
    reason: 'Review shared VLAN projection',
  });
  await call(page, 'complete_safe_apply', { decision: 'confirm' });
  await call(page, 'open_inventory_object', { target: 'Port/server-07' });
  await expect(heading(page, 'server-07')).toBeVisible();
  await expect(
    page.getByText('Trunk · 120, 240', { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole('button', { name: 'br-fabric', exact: true }).click();
  const child = page.getByRole('row').filter({
    has: page.getByRole('button', { name: 'server-07', exact: true }),
  });
  await expect(child).toContainText('Trunk · 120, 240');
  await expect(child).toContainText('enp129s0f1');
  const final = await read(page);
  expect(final.transaction.status).toBe('confirmed');
  expect(final.candidate).toBeNull();
});
