import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';

if (!process.env.OVS_FRONTEND_FIXTURE)
  throw new Error('Requires actual isolated Go/OVS services.');
const fixture = JSON.parse(
  readFileSync(process.env.OVS_FRONTEND_FIXTURE, 'utf8'),
);
const href =
  fixture.origin + '/switching/multicast/' + fixture.multicast.management_id;
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
  await expect(
    page.getByRole('heading', { name: 'br-mcast-ui', exact: true }),
  ).toBeVisible();
}
async function screen(page: Page, name: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: 'test-results/frontend-evidence/multicast-' + name + '.png',
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
async function apply(page: Page, desired: 'enabled' | 'disabled') {
  await page.goto(href);
  await page
    .getByLabel('Desired snooping', { exact: true })
    .selectOption(desired);
  await page
    .getByLabel('Entry aging time (seconds)', { exact: true })
    .fill('300');
  await page.getByLabel('Group table capacity', { exact: true }).fill('64');
  await page
    .getByLabel('Restrict unregistered multicast to router ports', {
      exact: true,
    })
    .selectOption('true');
  await page
    .getByRole('button', { name: 'Stage multicast change', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('table', {
      name: 'Original → Current → Yours · values supplied by the server',
    }),
  ).toContainText('mcast_snooping_enable');
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Diff & Validation', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      'Snooping changes multicast forwarding on this Bridge. Review group listeners, router ports and management connectivity before applying.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByText('MULTICAST_NATIVE_FORWARDING_REVIEW_REQUIRED', {
      exact: true,
    }),
  ).toHaveCount(0);
  await screen(page, 'validation-' + desired);
  await page
    .getByLabel('Change reason', { exact: true })
    .fill('Synthetic native multicast review');
  await page.getByRole('checkbox').check();
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(page).toHaveURL(/\/changes\/transactions\/[0-9a-f-]+$/);
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
  expect(vs('get', 'Bridge', 'br-mcast-ui', 'mcast_snooping_enable')).toBe(
    String(desired === 'enabled'),
  );
}

test('native multicast review keeps group-source availability, permissions and responsive responsibility explicit', async ({
  page,
}) => {
  await login(page, 'browser-multicast-reader');
  await page.goto(
    fixture.origin + '/switching/multicast?filter=br-mcast-ui&limit=1',
  );
  await expect(
    page.getByRole('table', { name: 'Observed Bridges in this page' }),
  ).toContainText('br-mcast-ui');
  await screen(page, 'list');
  await page.getByRole('link', { name: 'br-mcast-ui', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'br-mcast-ui', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Configuration unavailable:', { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText(
      'Your current permissions allow review but do not allow multicast changes.',
      { exact: false },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Stage multicast change', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText(
      'Snooping is disabled. The native group table is unavailable.',
      { exact: true },
    ),
  ).toBeVisible();
  await screen(page, 'reader-standard');
  await page.goto(
    fixture.origin +
      '/switching/multicast?filter=synthetic-no-multicast-match&limit=1',
  );
  await expect(
    page.getByText('No Bridges in this observation.', { exact: true }),
  ).toBeVisible();
  await screen(page, 'empty');
  await page.goto(href);
  await expect(
    page.getByRole('heading', { name: 'br-mcast-ui', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await screen(page, 'reader-expert');
  await page
    .getByRole('button', { name: 'Toggle color theme', exact: true })
    .click();
  await screen(page, 'reader-dark');
  for (const [width, height, label] of [
    [900, 1100, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByText('Review mode. Use a desktop to prepare a change.', {
        exact: true,
      }),
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
        'This observation is not current. Values are last observed; configuration changes are unavailable.',
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Stage multicast change', exact: true }),
    ).toHaveCount(0);
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
  await login(page, 'browser-multicast-observer');
  await expect(
    page.getByText('Observed configuration: Unavailable or withheld', {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole('region', {
      name: 'Multicast group observations',
      exact: true,
    }),
  ).toContainText(
    'Your current permissions do not allow review of this configuration.',
  );
  await expect(
    page.getByRole('button', { name: 'Stage multicast change', exact: true }),
  ).toHaveCount(0);
  await screen(page, 'observer-withheld');
});

test('native multicast uses shared confirmation, exact map compensation and explicit conflict restaging', async ({
  page,
}) => {
  await login(page, 'browser-multicast');
  await screen(page, 'editor');
  await apply(page, 'enabled');
  await decision(page, 'Confirm configuration', 'confirmed');
  expect(vs('get', 'Bridge', 'br-mcast-ui', 'mcast_snooping_enable')).toBe(
    'true',
  );
  expect(
    vs(
      'get',
      'Bridge',
      'br-mcast-ui',
      'other_config:mcast-snooping-aging-time',
    ),
  ).toBe('"300"');
  expect(
    vs('get', 'Bridge', 'br-mcast-ui', 'other_config:multicast-opaque'),
  ).toBe('preserve');
  await page.goto(href);
  await expect(
    page.getByText(
      'A qualified native group observation source is not configured. Group membership is unavailable.',
      { exact: true },
    ),
  ).toBeVisible();
  await screen(page, 'confirmed');
  await apply(page, 'disabled');
  await decision(page, 'Request rollback', 'rolled-back');
  expect(vs('get', 'Bridge', 'br-mcast-ui', 'mcast_snooping_enable')).toBe(
    'true',
  );
  const settled = vs('get', 'Open_vSwitch', '.', 'next_cfg');
  await page
    .getByRole('button', { name: 'Refresh evidence', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  expect(vs('get', 'Open_vSwitch', '.', 'next_cfg')).toBe(settled);
  await screen(page, 'recovered');
  vs('set', 'Bridge', 'br-mcast-ui', 'mcast_snooping_enable=false');
  const external = vs('get', 'Open_vSwitch', '.', 'next_cfg');
  await page.goto(fixture.origin + '/changes/candidate');
  await expect(
    page.getByRole('region', {
      name: 'Multicast change needs review',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText(
      'Live multicast configuration changed after capture. Discard this intent and review the current Bridge.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Validate Candidate', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Rebase reviewed choices', exact: true }),
  ).toHaveCount(0);
  expect(vs('get', 'Bridge', 'br-mcast-ui', 'mcast_snooping_enable')).toBe(
    'false',
  );
  expect(vs('get', 'Open_vSwitch', '.', 'next_cfg')).toBe(external);
  await screen(page, 'conflict');
});
