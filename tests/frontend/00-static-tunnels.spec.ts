import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';

if (!process.env.OVS_FRONTEND_FIXTURE)
  throw new Error('Requires actual isolated Go/OVS services.');
const fixture = JSON.parse(
  readFileSync(process.env.OVS_FRONTEND_FIXTURE, 'utf8'),
);
const base = fixture.origin + '/switching/tunnels';
const href = base + '/' + fixture.tunnel.management_id;
const vs = (...args: string[]) =>
  execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    { encoding: 'utf8' },
  ).trim();
async function login(page: Page, user: string) {
  await page.goto(href);
  await page.getByLabel('Username', { exact: true }).fill(user);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[user]);
  const reply = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' && r.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
  await page.goto(href);
}
async function screen(page: Page, name: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: 'test-results/frontend-evidence/tunnel-' + name + '.png',
    fullPage: true,
  });
}
async function decision(page: Page, name: string, state: string) {
  for (let review = 0; review < 4; review++) {
    const reply = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/decisions'),
    );
    await page.getByRole('button', { name, exact: true }).click();
    const response = await reply;
    if (response.status() === 202) {
      await expect(page.getByTestId('safe-state')).toHaveText(state);
      return;
    }
    const problem = await response.json();
    expect(response.status()).toBe(409);
    expect(problem.code).toBe('TRANSACTION_VERSION_CHANGED');
    expect(problem.command_effect).toBe('not-started');
    await expect(
      page.getByRole('button', { name: 'Recover original request' }),
    ).toHaveCount(0);
    await page
      .getByRole('button', { name: 'Refresh evidence', exact: true })
      .click();
  }
  throw new Error('Explicit decision did not stabilize');
}
async function apply(page: Page, label: string) {
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('table', {
      name: 'Original → Current → Yours · immutable objects and native settings',
    }),
  ).toContainText('tunnel');
  await screen(page, 'candidate-' + label);
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Diff & Validation', exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Change reason', { exact: true })
    .fill('Synthetic static tunnel review');
  await page.getByRole('checkbox').check();
  await screen(page, 'validation-' + label);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(page).toHaveURL(/\/changes\/transactions\/[0-9a-f-]+$/);
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
}

test('native static tunnel review separates configuration, permission, dynamic ownership and stale observations', async ({
  page,
}) => {
  await login(page, 'browser-tunnel-reader');
  await expect(
    page.getByRole('heading', { name: 'tun-ui-data', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      'Your current permissions allow review but do not allow tunnel changes.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Stage tunnel change', exact: true }),
  ).toHaveCount(0);
  await screen(page, 'reader-standard');
  await page.goto(base + '?filter=tun-ui-data&limit=1');
  await expect(
    page.getByRole('table', { name: 'Observed tunnel Ports in this page' }),
  ).toContainText('192.0.2.20');
  await screen(page, 'list');
  await page.goto(base + '?filter=synthetic-no-tunnel-match');
  await expect(
    page.getByText('No tunnel Ports in this observation.', { exact: true }),
  ).toBeVisible();
  await screen(page, 'empty');
  await page.goto(base + '?filter=tun-ui-flow');
  await page.getByRole('link', { name: 'tun-ui-flow', exact: true }).click();
  await expect(
    page.getByText(
      'This tunnel uses flow-controlled endpoints or keys. Review it through its owning controller; this page keeps it read-only.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Stage tunnel change', exact: true }),
  ).toHaveCount(0);
  await screen(page, 'dynamic-read-only');
  await page.goto(href);
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByText(
      'Interface.type and static remote_ip / local_ip / key / dst_port options',
      { exact: true },
    ),
  ).toBeVisible();
  await screen(page, 'expert');
  await page
    .getByRole('button', { name: 'Toggle color theme', exact: true })
    .click();
  await screen(page, 'dark');
  for (const [width, height, label] of [
    [900, 1100, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByText(
        'Review mode. Use a desktop to prepare a new tunnel change.',
        { exact: true },
      ),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await screen(page, label);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  execFileSync('ovs-appctl', ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit']);
  try {
    await expect(
      page.getByText('Observation: stale', { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByText(
        'Values are last observed. Configuration changes require a fresh native observation.',
        { exact: true },
      ),
    ).toBeVisible();
    await screen(page, 'stale');
  } finally {
    execFileSync(
      'ovsdb-server',
      [
        fixture.database,
        `--remote=punix:${fixture.dbSocket}`,
        `--pidfile=${fixture.ovsDirectory}/db.pid`,
        `--unixctl=${fixture.ovsDirectory}/db.ctl`,
        '--detach',
        '--no-chdir',
        '--overwrite-pidfile',
      ],
      {
        env: {
          ...process.env,
          OVS_RUNDIR: fixture.ovsDirectory,
          OVS_LOGDIR: fixture.ovsDirectory,
          OVS_DBDIR: fixture.ovsDirectory,
        },
        stdio: 'pipe',
      },
    );
  }
  await expect(
    page.getByText('Observation: fresh', { exact: false }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await login(page, 'browser-tunnel-observer');
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText('192.0.2.20', { exact: true })).toHaveCount(0);
  await screen(page, 'withheld');
});

test('native static tunnel CRUD uses shared review, exact compensation and fresh deletion identities', async ({
  page,
}) => {
  await login(page, 'browser-tunnel');
  await expect(
    page.getByRole('button', { name: 'Stage tunnel change', exact: true }),
  ).toBeVisible();
  await screen(page, 'editor');
  await page.goto(base + '/new');
  await page
    .getByLabel('Bridge', { exact: true })
    .selectOption(fixture.tunnelBridge.management_id);
  await page
    .getByLabel('New immutable tunnel name', { exact: true })
    .selectOption('tun-ui-new');
  await page.getByLabel('Tunnel type', { exact: true }).selectOption('geneve');
  await page
    .getByLabel('Remote IP address', { exact: true })
    .fill('192.0.2.21');
  await page.getByLabel('Remote IP address', { exact: true }).focus();
  await page.keyboard.press('Tab');
  await expect(page.getByLabel('Local IP address (optional)', { exact: true })).toBeFocused();
  await page.getByLabel('Key / VNI (optional)', { exact: true }).fill('3003');
  await page
    .getByLabel('I reviewed the endpoints and management connectivity.', {
      exact: true,
    })
    .check();
  await screen(page, 'creation');
  await page
    .getByRole('button', { name: 'Stage tunnel change', exact: true })
    .click();
  await apply(page, 'create');
  expect(vs('get', 'Interface', 'tun-ui-new', 'type')).toBe('geneve');
  await decision(page, 'Confirm configuration', 'confirmed');
  await page.goto(href);
  const original = vs(
    'get',
    'Interface',
    'tun-ui-data',
    'type',
    'options',
    'external_ids',
  );
  const oldPort = vs('get', 'Port', 'tun-ui-data', '_uuid');
  const oldIface = vs('get', 'Interface', 'tun-ui-data', '_uuid');
  await page.getByLabel('Key / VNI (optional)', { exact: true }).fill('2002');
  await page
    .getByLabel('I reviewed the endpoints and management connectivity.', {
      exact: true,
    })
    .check();
  await page
    .getByRole('button', { name: 'Stage tunnel change', exact: true })
    .click();
  await apply(page, 'configure');
  expect(vs('get', 'Interface', 'tun-ui-data', 'options:key')).toBe(
    '"2002"',
  );
  await decision(page, 'Request rollback', 'rolled-back');
  expect(
    vs(
      'get',
      'Interface',
      'tun-ui-data',
      'type',
      'options',
      'external_ids',
    ),
  ).toBe(original);
  await screen(page, 'configuration-recovered');
  await page.goto(fixture.origin + '/changes/candidate');
  await page
    .getByRole('button', { name: 'Discard intents…', exact: true })
    .click();
  const discarded = page.waitForResponse(
    r => r.request().method() === 'PATCH' && r.url().endsWith('/api/v1/candidate'),
  );
  await page
    .getByRole('button', { name: 'Confirm discard', exact: true })
    .click();
  expect((await discarded).status()).toBe(200);
  await expect(page.getByText('0 staged intents', { exact: true })).toBeVisible();
  await page.goto(href);
  await page
    .getByLabel(
      'I reviewed the tunnel deletion and its affected connectivity.',
      { exact: true },
    )
    .check();
  await page
    .getByRole('button', { name: 'Stage tunnel deletion', exact: true })
    .click();
  await apply(page, 'delete');
  expect(vs('--if-exists', 'get', 'Port', oldPort, '_uuid')).toBe('');
  await decision(page, 'Request rollback', 'rolled-back');
  expect(vs('get', 'Port', 'tun-ui-data', '_uuid')).not.toBe(oldPort);
  expect(vs('get', 'Interface', 'tun-ui-data', '_uuid')).not.toBe(
    oldIface,
  );
  expect(vs('get', 'Interface', 'tun-ui-data', 'options:key')).toBe(
    '"1001"',
  );
  expect(
    vs('get', 'Interface', 'tun-ui-data', 'external_ids:tunnel-foreign'),
  ).toBe('preserve');
  const settled = vs('get', 'Open_vSwitch', '.', 'next_cfg');
  await page
    .getByRole('button', { name: 'Refresh evidence', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  expect(vs('get', 'Open_vSwitch', '.', 'next_cfg')).toBe(settled);
  await screen(page, 'deletion-recovered');
  await page.goto(href);
  await expect(
    page.getByRole('button', { name: 'Stage tunnel change', exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText('Unavailable', { exact: true })).toBeVisible();
  await expect(
    page.getByText('NOT_FOUND. Refresh to read current evidence.', {
      exact: true,
    }),
  ).toBeVisible();
  await screen(page, 'retired-identity');
  await expect(
    page.getByText('NOT_FOUND. Refresh to read current evidence.', {
      exact: true,
    }),
  ).toBeVisible();

  await page.goto(base + '?filter=tun-ui-data');
  await page
    .getByRole('link', { name: 'tun-ui-data', exact: true })
    .click();
  await expect(
    page.getByText(
      'Local ownership or a supported Bridge and Port configuration is required before preparing a change.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Stage tunnel change', exact: true }),
  ).toHaveCount(0);
  await screen(page, 'fresh-identity-requires-grant');
});
