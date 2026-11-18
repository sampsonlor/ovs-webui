import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  accounts: Record<string, string>;
  telemetryWrite: { id: string; bridge: string };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));
const href = fixture.origin + '/visibility/telemetry';

function vsctl(...args: string[]) {
  return execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    { encoding: 'utf8' },
  ).trim();
}

async function login(page: Page, account: string) {
  expect((await page.goto(href))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const response = page.waitForResponse(
    (reply) =>
      reply.request().method() === 'POST' &&
      reply.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await response).status()).toBe(201);
}

async function capture(page: Page, state: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: `test-results/frontend-evidence/telemetry-${state}.png`,
    fullPage: true,
  });
}

test('Native telemetry reviews three Bridge exporters through authenticated responsive views', async ({
  page,
  browser,
}) => {
  const bridge = 'br-telem-ui';
  const privateMarker = 'synthetic-private-browser-telemetry';
  try {
    vsctl(
      'add-br',
      bridge,
      '--',
      '--id=@sf',
      'create',
      'sFlow',
      'targets="127.0.0.1:6343"',
      'sampling=200',
      'polling=0',
      'external_ids:fixture=' + privateMarker,
      '--',
      '--id=@nf',
      'create',
      'NetFlow',
      'targets="127.0.0.1:2055"',
      'active_timeout=60',
      '--',
      '--id=@ipf',
      'create',
      'IPFIX',
      'targets="127.0.0.1:4739"',
      'sampling=400',
      'obs_point_id=21',
      'other_config:fixture=' + privateMarker,
      '--',
      'set',
      'Bridge',
      bridge,
      'datapath_type=dummy',
      'sflow=@sf',
      'netflow=@nf',
      'ipfix=@ipf',
    );

    await login(page, 'browser-telemetry-reader');
    await page.goto(href);
    await expect(
      page.getByRole('heading', { name: 'Telemetry', exact: true }),
    ).toBeVisible();
    await page.getByLabel('Bridge name', { exact: true }).fill(bridge);
    await page.getByRole('button', { name: 'Filter Bridges' }).click();
    const link = page.getByRole('link', { name: bridge, exact: true });
    await expect(link).toBeVisible();
    await expect(
      page.getByRole('link', { name: 'br-inv', exact: true }),
    ).toHaveCount(0);
    const detailUrl = await link.getAttribute('href');
    expect(detailUrl).toMatch(/^\/visibility\/telemetry\/[0-9a-f-]{36}$/);
    const id = detailUrl!.split('/').at(-1)!;
    await capture(page, 'list');
    await link.focus();
    await expect(link).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(fixture.origin + detailUrl);

    const sflow = page.getByRole('article', { name: 'sFlow', exact: true });
    const netflow = page.getByRole('article', { name: 'NetFlow', exact: true });
    const ipfix = page.getByRole('article', { name: 'IPFIX', exact: true });
    for (const [panel, endpoint] of [
      [sflow, '127.0.0.1:6343'],
      [netflow, '127.0.0.1:2055'],
      [ipfix, '127.0.0.1:4739'],
    ] as const) {
      await expect(panel).toContainText('Configured');
      await expect(panel).toContainText(endpoint);
    }
    await expect(
      page.getByText('Collector delivery is unverified.', { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(privateMarker)).toHaveCount(0);
    await expect(page.getByText('Native UUID', { exact: true })).toHaveCount(0);
    await expect(
      page.getByRole('button', { name: /stage|apply|configure/i }),
    ).toHaveCount(0);
    await capture(page, 'standard');

    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await expect(sflow.getByText('Native UUID', { exact: true })).toBeVisible();
    await expect(sflow).toContainText(vsctl('get', 'Bridge', bridge, 'sflow'));
    await expect(
      page.getByRole('button', { name: /stage|apply|configure/i }),
    ).toHaveCount(0);
    await capture(page, 'expert');

    vsctl('clear', 'Bridge', bridge, 'netflow');
    const detailPath = fixture.origin + '/api/v1/telemetry/' + id;
    await expect
      .poll(async () => {
        const response = await page.request.get(detailPath);
        if (response.status() !== 200) return 'unavailable';
        return (await response.json()).native_telemetry.exporters.netflow.state;
      })
      .toBe('unconfigured');
    const refresh = page.getByRole('button', { name: 'Refresh telemetry' });
    await refresh.click();
    await expect(netflow).toContainText('Not configured');
    await expect(sflow).toContainText('127.0.0.1:6343');
    await expect(ipfix).toContainText('127.0.0.1:4739');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const width of [820, 390]) {
      await page.setViewportSize({ width, height: 844 });
      await expect(netflow).toContainText('Not configured');
      await expect(
        page.getByRole('link', { name: 'Review Bridge', exact: true }),
      ).toBeVisible();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth),
      ).toBeLessThanOrEqual(width);
    }
    await capture(page, 'mobile');

    const observer = await browser.newPage({ ignoreHTTPSErrors: true });
    try {
      await login(observer, 'browser-telemetry-observer');
      await observer.goto(fixture.origin + detailUrl);
      await expect(
        observer.getByText('Permission denied', { exact: true }),
      ).toBeVisible();
      await expect(observer.getByText('127.0.0.1:6343')).toHaveCount(0);
      expect((await observer.request.get(detailPath)).status()).toBe(403);
      await capture(observer, 'withheld');
    } finally {
      await observer.close();
    }

    // Actual DB loss and recovery are checked in the daemon inventory fixture.
    // Test the UI's unavailable projection without stopping the shared web service.
    const unavailable = page.waitForResponse(
      (response) => response.url() === detailPath && response.status() === 503,
    );
    await page.route(detailPath, (route) =>
      route.fulfill({
        status: 503,
        json: { code: 'OVSDB_UNAVAILABLE', status: 503 },
      }),
    );
    await refresh.click();
    await unavailable;
    await expect(
      page.getByRole('status').getByText('Unavailable', { exact: true }),
    ).toBeVisible();
    await expect(sflow).toHaveCount(0);
    await capture(page, 'unavailable');
    await page.unroute(detailPath);
    await refresh.click();
    await expect(ipfix).toContainText('127.0.0.1:4739');
  } finally {
    vsctl('--no-wait', '--if-exists', 'del-br', bridge);
  }
});

test('Authorized NetFlow collector editing stages, applies and restores the exact Bridge configuration', async ({
  page,
  browser,
}) => {
  const user = 'browser-telemetry-writer';
  const { id, bridge } = fixture.telemetryWrite;
  const detail = href + '/' + id;
  const netflow = (view: Page) =>
    view.getByRole('article', { name: 'NetFlow', exact: true });
  const configure = (view: Page) =>
    netflow(view).getByRole('button', {
      name: 'Configure NetFlow',
      exact: true,
    });
  const original = '127.0.0.1:2055';
  const reviewed = '127.0.0.1:2056';
  const uuid = vsctl('get', 'Bridge', bridge, 'netflow');
  const checkNative = (target: string, timeout: number) => {
    expect(vsctl('get', 'Bridge', bridge, 'netflow')).toBe(uuid);
    expect(vsctl('get', 'NetFlow', uuid, 'targets')).toBe(
      '[' + JSON.stringify(target) + ']',
    );
    expect(vsctl('get', 'NetFlow', uuid, 'active_timeout')).toBe(
      String(timeout),
    );
    expect(vsctl('get', 'NetFlow', uuid, 'external_ids:fixture')).toBe(
      'preserve',
    );
  };

  const reader = await browser.newPage({ ignoreHTTPSErrors: true });
  try {
    await login(reader, 'browser-telemetry-reader');
    await reader.goto(detail);
    await expect(netflow(reader)).toContainText(original);
    await expect(configure(reader)).toHaveCount(0);
  } finally {
    await reader.close();
  }

  await login(page, user);
  await page.goto(detail);
  await expect(netflow(page)).toContainText(original);
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(configure(page)).toHaveCount(0);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(
    page.getByRole('button', { name: /Configure (sFlow|IPFIX)/ }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await configure(page).click();
  const targets = netflow(page).getByLabel('Collector targets');
  await expect(targets).toHaveValue(original);
  await targets.fill(reviewed);
  await netflow(page).getByLabel('Active timeout (s)').fill('30');
  await netflow(page).getByRole('button', { name: 'Review changes' }).click();
  await expect(netflow(page)).toContainText(reviewed);
  checkNative(original, 60);
  await capture(page, 'write-review');

  await netflow(page)
    .getByRole('button', { name: 'Stage NetFlow configuration' })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace' }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  await expect(diff).toContainText(reviewed);
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByText('Usable for this revision', { exact: true }),
  ).toBeVisible();
  checkNative(original, 60);
  await page.getByLabel('Verify your password').fill(fixture.accounts[user]);
  await page
    .getByRole('button', { name: 'Verify identity', exact: true })
    .click();
  await page
    .getByLabel('Change reason')
    .fill('Move the isolated NetFlow collector');
  await page.getByRole('checkbox', { name: /^I reviewed this Diff/ }).check();
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
  checkNative(reviewed, 30);
  await capture(page, 'write-applied');

  await page
    .getByRole('button', { name: 'Request rollback', exact: true })
    .click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  checkNative(original, 60);
  await capture(page, 'write-rolled-back');
});

test.afterAll(() => {
  // Retire the Bridge provisioned for the telemetry grant before later tests
  // inspect the bounded native topology of the shared browser environment.
  vsctl('--if-exists', 'del-br', fixture.telemetryWrite.bridge);
});
