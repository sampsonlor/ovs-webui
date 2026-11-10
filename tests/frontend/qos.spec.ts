import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';

const path = process.env.OVS_FRONTEND_FIXTURE;
if (!path) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  accounts: Record<string, string>;
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

test('Native QoS reads shared Port bindings, explicit Queue zero and a restricted view', async ({
  page,
  browser,
}) => {
  const ports = ['inv-p1', 'inv-p2'];
  for (const port of ports)
    expect(vsctl('get', 'Port', port, 'qos')).toBe('[]');
  const byLabel = (table: string) =>
    vsctl(
      '--bare',
      '--columns=_uuid',
      'find',
      table,
      'external_ids:synthetic-qos-review=fixture',
    )
      .split(/\s+/)
      .filter(Boolean);

  try {
    // A disposable database fixture for configuration reads. No queue
    // installation or measured traffic result is inferred from these rows.
    vsctl(
      '--',
      '--id=@queue',
      'create',
      'Queue',
      'dscp=0',
      'other_config:min-rate=100000',
      'other_config:max-rate=200000',
      'external_ids:synthetic-qos-review=fixture',
      'other_config:private-fixture=synthetic-private-qos',
      '--',
      '--id=@qos',
      'create',
      'QoS',
      'type=linux-htb',
      'queues:0=@queue',
      'other_config:max-rate=300000',
      'external_ids:synthetic-qos-review=fixture',
      '--',
      'set',
      'Port',
      ports[0],
      'qos=@qos',
      '--',
      'set',
      'Port',
      ports[1],
      'qos=@qos',
    );
    vsctl(
      'create',
      'QoS',
      'type=linux-sfq',
      'external_ids:synthetic-qos-review=fixture',
    );

    await login(page, 'browser-qos-reader');
    const list = page.getByRole('table', {
      name: 'Observed QoS configurations',
    });
    const shared = list.getByRole('row').filter({ hasText: 'linux-htb' });
    await expect(shared).toContainText('Shared across Ports');
    await expect(
      list.getByRole('row').filter({ hasText: 'linux-sfq' }),
    ).toContainText('Unbound');
    await shared.getByRole('link').click();
    const queues = page.getByRole('table', {
      name: 'Queue numbers within this QoS configuration',
    });
    const defaultQueue = queues
      .getByRole('row')
      .filter({ hasText: '0 (default)' });
    await expect(defaultQueue).toContainText('100000 bit/s');
    await expect(defaultQueue).toContainText('200000 bit/s');
    await expect(
      defaultQueue.getByRole('cell', { name: '0', exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(
        'Scheduler installation and Queue counters are unavailable',
        { exact: false },
      ),
    ).toBeVisible();
    await expect(page.getByText('synthetic-private-qos')).toHaveCount(0);

    mkdirSync('test-results/frontend-evidence', { recursive: true });
    await page.screenshot({
      path: 'test-results/frontend-evidence/qos-shared.png',
      fullPage: true,
    });
    await page.getByRole('link', { name: ports[0], exact: true }).click();
    await page.getByRole('link', { name: 'Review bound QoS' }).click();
    await expect(queues).toBeVisible();
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    await expect(
      queues.getByRole('columnheader', { name: 'Management ID' }),
    ).toBeVisible();
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(queues).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(390);
    await page.screenshot({
      path: 'test-results/frontend-evidence/qos-mobile.png',
      fullPage: true,
    });

    const restricted = await browser.newPage({ ignoreHTTPSErrors: true });
    try {
      await login(restricted, 'browser-qos-observer');
      await expect(
        restricted.getByText('Permission denied', { exact: true }),
      ).toBeVisible();
      await expect(
        restricted.getByRole('table', { name: 'Observed QoS configurations' }),
      ).toHaveCount(0);
      await restricted.screenshot({
        path: 'test-results/frontend-evidence/qos-withheld.png',
        fullPage: true,
      });
    } finally {
      await restricted.close();
    }
  } finally {
    const qos = byLabel('QoS');
    for (const port of ports) {
      const reference = vsctl('get', 'Port', port, 'qos');
      if (qos.some((id) => reference.includes(id)))
        vsctl('clear', 'Port', port, 'qos');
    }
    for (const id of qos) vsctl('destroy', 'QoS', id);
    for (const id of byLabel('Queue')) vsctl('destroy', 'Queue', id);
  }
});
