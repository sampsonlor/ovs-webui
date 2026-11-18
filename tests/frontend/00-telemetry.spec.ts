import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  accounts: Record<string, string>;
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
