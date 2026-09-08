import type { Page, TestInfo } from '@playwright/test';
import type { ControlState } from '../../lib/change-control';
import { test, expect } from './fixtures';

test.use({ serverMode: 'prototype' });

type Tool = { name: string; execute: (input: unknown) => unknown };
type ReviewWindow = Window & { prototypeTools: Map<string, Tool> };
type State = ControlState & {
  diagnostic: { state: string; scope: string; job: string | null };
  health: { overall: string };
};

async function call<T = unknown>(
  page: Page,
  name: string,
  input: unknown = {},
): Promise<T> {
  return page.evaluate(
    async ({ toolName, args }) => {
      const tool = (window as unknown as ReviewWindow).prototypeTools.get(
        toolName,
      );
      if (!tool) throw new Error(`Unregistered prototype tool: ${toolName}`);
      return await tool.execute(args);
    },
    { toolName: name, args: input },
  ) as Promise<T>;
}
const state = (page: Page) => call<State>(page, 'read_prototype_state');
const bond = {
  name: 'bond-integration',
  bridge: 'br-fabric',
  mode: 'balance-tcp',
  lacp: 'active',
};

async function open(page: Page) {
  // Capture the real public tool registrations; do not inject application state.
  await page.addInitScript(() => {
    const tools = new Map<string, Tool>();
    (window as unknown as ReviewWindow).prototypeTools = tools;
    Object.defineProperty(document, 'modelContext', {
      configurable: true,
      value: {
        registerTool(tool: Tool, options: { signal: AbortSignal }) {
          tools.set(tool.name, tool);
          options.signal.addEventListener('abort', () =>
            tools.delete(tool.name),
          );
        },
      },
    });
  });
  await page.goto('/');
  await page.waitForFunction(() =>
    (window as unknown as ReviewWindow).prototypeTools.has(
      'set_health_review_state',
    ),
  );
  await expect(
    page.getByRole('button', {
      name: 'Navigation capability states',
      exact: true,
    }),
  ).toContainText('Available · 1');
}

async function capture(page: Page, info: TestInfo, name: string) {
  if (process.env.OVS_BROWSER_CAPTURE === '1')
    await page.screenshot({
      path: info.outputPath(`${name}.png`),
      fullPage: true,
    });
}

test('Bond UI and registered tool reject occupied inventory and produce the same native Candidate', async ({
  page,
}, info) => {
  await open(page);
  for (const input of [
    { ...bond, name: 'uplink-01' },
    { ...bond, bridge: 'br-storage' },
    { ...bond, bridge: 'br-mgmt' },
    { ...bond, members: ['enp65s0f0', 'enp65s0f1'] },
    { ...bond, minLinks: 3 },
  ])
    await expect(call(page, 'stage_bond_change', input)).rejects.toThrow();
  expect((await state(page)).candidate).toBeNull();
  await page.getByRole('button', { name: 'Switching', exact: true }).click();
  await page.getByRole('button', { name: 'Bonds / LACP', exact: true }).click();
  await page
    .getByRole('button', { name: 'New bond intent', exact: true })
    .click();
  await page.getByLabel('Bond Port name', { exact: true }).fill('uplink-01');
  await expect(
    page.getByText(
      'This Port name already exists in the synthetic inventory.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Add to workspace', exact: true }),
  ).toBeDisabled();
  await page.getByLabel('Bond Port name', { exact: true }).fill(bond.name);
  await page.getByLabel('Minimum active links', { exact: true }).fill('2');
  await page
    .getByRole('button', { name: 'Add to workspace', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate workspace', exact: true }),
  ).toBeVisible();
  const ui = (await state(page)).candidate;
  const result = await call<{ candidate: State['candidate'] }>(
    page,
    'stage_bond_change',
    { ...bond, minLinks: 2, members: ['enp65s0f3', 'enp65s0f2'] },
  );
  expect(result.candidate?.kind).toBe('bond');
  if (result.candidate?.kind !== 'bond' || ui?.kind !== 'bond')
    throw new Error('Expected native Bond candidates');
  expect(result.candidate.intent).toEqual(ui.intent);
  expect(result.candidate.intent.candidate).toContain(
    'other_config:min-links: 2',
  );
  await capture(page, info, 'bond-shared-candidate');
});

test('an unknown Bond transaction remains locked across all observation domains and an independent diagnostic Job', async ({
  page,
}, info) => {
  await open(page);
  await call(page, 'stage_bond_change', bond);
  await call(page, 'validate_workspace');
  await call(page, 'start_safe_apply', {
    reason: 'Review shared P1 ownership',
  });
  await call(page, 'set_review_scenario', { scenario: 'outcome-unknown' });
  const before = await state(page);
  expect(before.transaction.status).toBe('outcome-unknown');
  await call(page, 'run_bounded_diagnostic', {
    diagnostic: 'diag.net.link-lacp',
    scope: 'Port/bond-storage',
  });
  await call(page, 'set_openflow_review_state', { state: 'fresh' });
  await expect(
    page.getByRole('heading', { name: 'OpenFlow Viewer', exact: true }),
  ).toBeVisible();
  await call(page, 'set_acceleration_review_state', { state: 'normal' });
  await expect(
    page.getByRole('heading', { name: 'DPDK / Offload', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeEnabled();
  await call(page, 'set_health_review_state', { state: 'healthy' });
  await expect(
    page.getByRole('button', { name: 'Refresh health', exact: true }),
  ).toBeEnabled();
  await expect
    .poll(async () => (await state(page)).health.overall)
    .toBe('Recovery Required');
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await capture(page, info, 'unknown-transaction-health');
  await expect
    .poll(async () => (await state(page)).diagnostic.state, { timeout: 15_000 })
    .toBe('complete');
  const after = await state(page);
  expect(after.transaction).toEqual(before.transaction);
  expect(after.candidate).toEqual(before.candidate);
  expect(after.live).toEqual(before.live);
  expect(after.diagnostic.scope).toBe('Port/bond-storage');
  expect(
    after.evidence.filter(
      (entry) =>
        entry.kind === 'Audit' &&
        entry.correlation === before.transaction.correlation,
    ),
  ).toEqual(
    before.evidence.filter(
      (entry) =>
        entry.kind === 'Audit' &&
        entry.correlation === before.transaction.correlation,
    ),
  );
  expect(
    after.evidence.some(
      (entry) =>
        entry.kind === 'Event' &&
        entry.object === 'Port/bond-storage' &&
        entry.correlation === 'corr-DIAG-91C4',
    ),
  ).toBe(true);
  for (const [name, input] of [
    ['complete_safe_apply', { decision: 'confirm' }],
    ['stage_bond_change', { ...bond, name: 'bond-second' }],
    [
      'stage_vlan_change',
      { port: 'server-07', vlanMode: 'trunk', allowedVlans: '120,240' },
    ],
  ] as const)
    await expect(call(page, name, input)).rejects.toThrow();
  await page
    .getByRole('button', { name: 'Events / Audit', exact: true })
    .click();
  await expect(
    page.getByText('Synthetic diagnostic · job-3114 · Complete', {
      exact: true,
    }),
  ).toBeVisible();
});

test('Bond configuration stays desktop-only while tablet and mobile can handle the original Safe Apply', async ({
  page,
}, info) => {
  await open(page);
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(call(page, 'stage_bond_change', bond)).rejects.toThrow(
      /desktop/,
    );
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await call(page, 'stage_bond_change', bond);
  await call(page, 'validate_workspace');
  const transaction = await call<State['transaction']>(
    page,
    'start_safe_apply',
    { reason: 'Review incident companion recovery' },
  );
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(
      page.getByRole('button', { name: 'Confirm configuration', exact: true }),
    ).toBeEnabled();
    await expect(
      page.getByRole('button', { name: 'Roll back now', exact: true }),
    ).toBeEnabled();
    await expect(
      call(page, 'stage_bond_change', { ...bond, name: 'bond-second' }),
    ).rejects.toThrow();
    expect((await state(page)).transaction.id).toBe(transaction.id);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await capture(page, info, `bond-safe-apply-${width}`);
  }
  await page
    .getByRole('button', { name: 'Confirm configuration', exact: true })
    .click();
  const final = await state(page);
  expect(final.transaction.status).toBe('confirmed');
  expect(final.transaction.id).toBe(transaction.id);
  expect(final.candidate).toBeNull();
  const confirmation = final.evidence.find(
    (entry) =>
      entry.kind === 'Audit' &&
      entry.correlation === transaction.correlation &&
      entry.text.startsWith('Safe Apply confirmed'),
  );
  expect(confirmation?.object).toBe('Port/bond-integration');
});
