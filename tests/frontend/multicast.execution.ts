import { test, expect } from '@playwright/test';
import { execFileSync, spawn } from 'node:child_process';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';

if (!process.env.OVS_MULTICAST_FRONTEND_FIXTURE)
  throw new Error(
    'Requires qualified native multicast source and isolated production services.',
  );
const fixture = JSON.parse(
  readFileSync(process.env.OVS_MULTICAST_FRONTEND_FIXTURE, 'utf8'),
);
const href =
  fixture.origin + '/switching/multicast/' + fixture.bridge.management_id;
const output = 'test-results/ovs-witness/multicast-frontend-evidence';

test('qualified native multicast membership shows actual IGMP Port binding and refuses an unproven build', async ({
  page,
}) => {
  await page.goto(href);
  await page
    .getByLabel('Username', { exact: true })
    .fill('native-multicast-reader');
  await page.getByLabel('Password', { exact: true }).fill(fixture.password);
  const response = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' && r.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await response).status()).toBe(201);
  await page.goto(href);
  await expect(
    page.getByRole('heading', { name: 'br-mcast-live', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('No groups in this native observation.', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Stage multicast change', exact: true }),
  ).toHaveCount(0);
  mkdirSync(output, { recursive: true });
  await page.screenshot({
    path: output + '/native-multicast-empty.png',
    fullPage: true,
  });
  const listener = spawn(
    'python3',
    [
      '-u',
      '-c',
      `import socket,time
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('',20002));s.setsockopt(socket.IPPROTO_IP,socket.IP_ADD_MEMBERSHIP,socket.inet_aton('239.192.0.11')+socket.inet_aton('198.19.204.1'))
print('ready',flush=True);time.sleep(60)
`,
    ],
    { stdio: ['ignore', 'pipe', 'pipe'] },
  );
  try {
    await new Promise<void>((resolve, reject) => {
      listener.once('error', reject);
      listener.stdout.once('data', (data) =>
        String(data).trim() === 'ready'
          ? resolve()
          : reject(new Error('Synthetic listener did not become ready')),
      );
      listener.once('exit', (code) =>
        reject(new Error('Synthetic listener exited: ' + code)),
      );
    });
    const table = page.getByRole('table', {
      name: 'Native group and router Port observations',
      exact: true,
    });
    await expect(table).toContainText('239.192.0.11');
    await expect(table).toContainText('IGMPv2');
    await expect(
      table.getByRole('link', { name: 'mcast-live-p', exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: output + '/native-multicast-standard.png',
      fullPage: true,
    });
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await expect(table).toContainText('ofport');
    await page.screenshot({
      path: output + '/native-multicast-expert.png',
      fullPage: true,
    });
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await page.screenshot({
      path: output + '/native-multicast-dark.png',
      fullPage: true,
    });
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    for (const [width, height, name] of [
      [900, 1100, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await expect(table).toContainText('239.192.0.11');
      await expect(
        page.getByRole('button', {
          name: 'Stage multicast change',
          exact: true,
        }),
      ).toHaveCount(0);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
      await page.screenshot({
        path: output + '/native-multicast-' + name + '.png',
        fullPage: true,
      });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    expect(fixture.unit).toMatch(/^ovs-mgrd-inventory-[0-9a-f]+\.service$/);
    expect(fixture.unitPath).toBe('/etc/systemd/system/' + fixture.unit);
    const original = readFileSync(fixture.unitPath, 'utf8');
    expect(original).toContain(
      '--multicast-group-witness-manifest-sha256=' + fixture.manifestDigest,
    );
    try {
      execFileSync('systemctl', ['is-active', '--quiet', fixture.unit]);
      execFileSync('systemctl', ['stop', fixture.unit]);
      writeFileSync(
        fixture.unitPath,
        original.replace(
          '--multicast-group-witness-manifest-sha256=' + fixture.manifestDigest,
          '--multicast-group-witness-manifest-sha256=' + '0'.repeat(64),
        ),
      );
      execFileSync('systemctl', ['daemon-reload']);
      execFileSync('systemctl', ['reset-failed', fixture.unit]);
      execFileSync('systemctl', ['start', fixture.unit]);
      await expect(
        page.getByText(
          'The native group observation could not be verified. Group membership is unavailable.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        page.getByRole('table', {
          name: 'Native group and router Port observations',
          exact: true,
        }),
      ).toHaveCount(0);
      await expect(
        page.getByText('No groups in this native observation.', {
          exact: true,
        }),
      ).toHaveCount(0);
      await page.screenshot({
        path: output + '/native-multicast-source-unavailable.png',
        fullPage: true,
      });
    } finally {
      execFileSync('systemctl', ['stop', fixture.unit]);
      writeFileSync(fixture.unitPath, original);
      execFileSync('systemctl', ['daemon-reload']);
      execFileSync('systemctl', ['reset-failed', fixture.unit]);
      execFileSync('systemctl', ['start', fixture.unit]);
    }
    await expect(
      page.getByRole('table', {
        name: 'Native group and router Port observations',
        exact: true,
      }),
    ).toContainText('239.192.0.11');
  } finally {
    listener.kill();
  }
});
