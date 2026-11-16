import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import type { Resource } from '../../clients/typescript/public-v1.generated';

const path = process.env.OVS_FRONTEND_FIXTURE;
if (!path) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  ovsDirectory: string;
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
    const qosID = new URL(page.url()).pathname.split('/').at(-1);
    expect(qosID).toMatch(/^[0-9a-f-]{36}$/);
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
        'A single Port binding is required for scheduler and Queue observation.',
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
    const portQoS = page.getByRole('link', { name: 'Review bound QoS' });
    await expect(portQoS).toBeVisible();
    const portID = new URL(page.url()).pathname.split('/').at(-1);
    expect(
      (
        await page.request.get(`${fixture.origin}/api/v1/qos/${portID}`)
      ).status(),
    ).toBe(404);
    await portQoS.click();
    await expect(queues).toBeVisible();
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
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
      expect(
        (
          await restricted.request.get(`${fixture.origin}/api/v1/qos/${qosID}`)
        ).status(),
      ).toBe(403);
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

test('Native QoS observes Linux classes and per-Queue counters after isolated traffic', async ({
  page,
}) => {
  const bridge = 'br-qos-runtime';
  const sender = 'qos-tx-host';
  const receiver = 'qos-rx-host';
  const sendNS = 'ovs-qos-tx';
  const recvNS = 'ovs-qos-rx';
  const command = (file: string, ...args: string[]) =>
    execFileSync(file, args, { encoding: 'utf8', timeout: 5000 }).trim();
  const createdNS: string[] = [];
  const interfaces: string[] = [];
  const tagged = (table: string) =>
    vsctl(
      '--bare',
      '--columns=_uuid',
      'find',
      table,
      'external_ids:synthetic-qos-runtime=fixture',
    )
      .split(/\s+/)
      .filter(Boolean);
  try {
    for (const [host, namespace, address] of [
      [sender, sendNS, '198.18.71.1'],
      [receiver, recvNS, '198.18.71.2'],
    ]) {
      command('ip', 'netns', 'add', namespace);
      createdNS.push(namespace);
      command(
        'ip',
        'link',
        'add',
        host,
        'type',
        'veth',
        'peer',
        'name',
        'qos-peer',
        'netns',
        namespace,
      );
      interfaces.push(host);
      command('ip', 'link', 'set', host, 'up');
      command('ip', '-n', namespace, 'link', 'set', 'qos-peer', 'up');
      command(
        'ip',
        '-n',
        namespace,
        'addr',
        'add',
        address + '/24',
        'dev',
        'qos-peer',
      );
    }
    vsctl(
      'add-br',
      bridge,
      '--',
      'set',
      'Bridge',
      bridge,
      'datapath_type=system',
      '--',
      'add-port',
      bridge,
      sender,
      '--',
      'add-port',
      bridge,
      receiver,
    );
    const input = vsctl('get', 'Interface', sender, 'ofport');
    expect(input).toMatch(/^[1-9][0-9]*$/);
    command(
      'ovs-ofctl',
      'add-flow',
      `unix:${fixture.ovsDirectory}/${bridge}.mgmt`,
      `priority=100,in_port=${input},actions=set_queue:7,normal`,
    );
    vsctl(
      '--',
      '--id=@queue',
      'create',
      'Queue',
      'other_config:min-rate=1000000',
      'other_config:max-rate=3000000',
      'external_ids:synthetic-qos-runtime=fixture',
      '--',
      '--id=@qos',
      'create',
      'QoS',
      'type=linux-htb',
      'queues:7=@queue',
      'other_config:max-rate=8000000',
      'external_ids:synthetic-qos-runtime=fixture',
      '--',
      'set',
      'Port',
      receiver,
      'qos=@qos',
    );
    await login(page, 'browser-qos-runtime');
    const list = page.getByRole('table', {
      name: 'Observed QoS configurations',
    });
    await list
      .getByRole('row')
      .filter({ hasText: 'linux-htb' })
      .getByRole('link')
      .click();
    const detail = new URL(page.url()).pathname.split('/').at(-1);
    expect(detail).toMatch(/^[0-9a-f-]{36}$/);
    const readResource = async () => {
      const reply = await page.request.get(
        `${fixture.origin}/api/v1/qos/${detail}`,
      );
      expect(reply.status()).toBe(200);
      return (await reply.json()) as Resource;
    };
    const read = async () => (await readResource()).linux_qos;
    const counters = page.getByRole('table', {
      name: 'Cumulative Linux Queue counters',
    });
    await expect(
      counters.getByRole('rowheader', { name: '7', exact: true }),
    ).toBeVisible();
    const before = await read();
    expect(before?.scheduler).toBe('observed');
    expect(before?.root_kind).toBe('htb');
    expect(before?.queues.map((q) => q.queue_id)).toEqual([0, 7]);
    const expectRates = async (
      root: string,
      minimum: string,
      maximum: string,
    ) => {
      await expect
        .poll(async () => {
          const installed = (await read())?.installed_rates;
          return {
            root: installed?.maximum_bps,
            queue: installed?.queues.find((q) => q.queue_id === 7),
          };
        })
        .toEqual({
          root,
          queue: { queue_id: 7, minimum_bps: minimum, maximum_bps: maximum },
        });
    };
    await expectRates('8000000', '1000000', '3000000');
    const old = before?.queues.find((q) => q.queue_id === 7)?.packets;
    expect(old).toMatch(/^\d+$/);
    command(
      'ip',
      'netns',
      'exec',
      sendNS,
      'ping',
      '-n',
      '-c',
      '3',
      '-i',
      '0.1',
      '-W',
      '1',
      '198.18.71.2',
    );
    await expect
      .poll(async () => {
        const current = (await read())?.queues.find((q) => q.queue_id === 7);
        return (
          current?.packets != null &&
          BigInt(current.packets) >= BigInt(old!) + BigInt(3) &&
          current.bytes != null &&
          BigInt(current.bytes) > BigInt(0)
        );
      })
      .toBe(true);
    await page.reload();
    await expect(
      counters.getByRole('rowheader', { name: '7', exact: true }),
    ).toBeVisible();
    const installedTable = page.getByRole('table', {
      name: 'Installed Linux Queue rates',
    });
    await expect(
      installedTable.getByRole('row').filter({
        has: page.getByRole('rowheader', { name: '7', exact: true }),
      }),
    ).toContainText('3000000 bit/s');
    await page.screenshot({
      path: 'test-results/frontend-evidence/qos-runtime-htb.png',
      fullPage: true,
    });

    const [qos] = tagged('QoS');
    const [queue] = tagged('Queue');
    const setRates = (
      root: number,
      minimum: number,
      maximum: number,
      wait = true,
    ) =>
      vsctl(
        ...(wait ? [] : ['--no-wait']),
        'set',
        'QoS',
        qos,
        `other_config:max-rate=${root}`,
        '--',
        'set',
        'Queue',
        queue,
        `other_config:min-rate=${minimum}`,
        `other_config:max-rate=${maximum}`,
      );
    const pid = Number(
      readFileSync(fixture.ovsDirectory + '/switch.pid', 'utf8').trim(),
    );
    process.kill(pid, 'SIGSTOP');
    try {
      setRates(12000000, 2000000, 4000000, false);
      await expect
        .poll(async () => {
          const sample = await readResource();
          return {
            configured: sample.native_qos?.max_rate.bps,
            installed: sample.linux_qos?.installed_rates?.maximum_bps,
            queue: sample.linux_qos?.installed_rates?.queues.find(
              (q) => q.queue_id === 7,
            )?.maximum_bps,
          };
        })
        .toEqual({
          configured: '12000000',
          installed: '8000000',
          queue: '3000000',
        });
    } finally {
      process.kill(pid, 'SIGCONT');
    }
    await expectRates('12000000', '2000000', '4000000');
    setRates(8000000, 1000000, 3000000);
    await expectRates('8000000', '1000000', '3000000');

    // An external class makes the installed class tree incomplete from the
    // application's perspective. The known class counters remain useful.
    command(
      'tc',
      'class',
      'add',
      'dev',
      receiver,
      'parent',
      '1:fffe',
      'classid',
      '1:10',
      'htb',
      'rate',
      '1mbit',
      'ceil',
      '2mbit',
    );
    await expect
      .poll(async () => {
        const sample = await read();
        return {
          rates: sample?.installed_rates ?? null,
          counters: sample?.queues.map((q) => q.queue_id),
        };
      })
      .toEqual({ rates: null, counters: [0, 7] });
    command('tc', 'class', 'del', 'dev', receiver, 'classid', '1:10');
    await expectRates('8000000', '1000000', '3000000');

    vsctl('set', 'QoS', qos, 'type=linux-hfsc');
    await expect
      .poll(async () => {
        const current = await read();
        return {
          scheduler: current?.scheduler,
          root: current?.root_kind,
          queues: current?.queues.map((q) => q.queue_id),
        };
      })
      .toEqual({ scheduler: 'observed', root: 'hfsc', queues: [0, 7] });
    await expectRates('8000000', '1000000', '3000000');
    setRates(12000000, 2000000, 4000000);
    await expectRates('12000000', '2000000', '4000000');
    setRates(8000000, 1000000, 3000000);
    await expectRates('8000000', '1000000', '3000000');
    let hfscBefore: string | null | undefined;
    await expect
      .poll(async () => {
        const current = await read();
        hfscBefore = current?.queues.find((q) => q.queue_id === 7)?.packets;
        return current?.installed_rates?.maximum_bps === '8000000'
          ? hfscBefore
          : undefined;
      })
      .toMatch(/^\d+$/);
    command(
      'ip',
      'netns',
      'exec',
      sendNS,
      'ping',
      '-n',
      '-c',
      '3',
      '-i',
      '0.1',
      '-W',
      '1',
      '198.18.71.2',
    );
    await expect
      .poll(async () => {
        const q = (await read())?.queues.find((q) => q.queue_id === 7);
        return (
          q?.packets != null &&
          BigInt(q.packets) >= BigInt(hfscBefore!) + BigInt(3)
        );
      })
      .toBe(true);
    await expect(
      page.getByText('Observed · hfsc', { exact: true }),
    ).toBeVisible();
    await expect(
      installedTable.getByRole('row').filter({
        has: page.getByRole('rowheader', { name: '7', exact: true }),
      }),
    ).toContainText('1000000 bit/s');
    await page.screenshot({
      path: 'test-results/frontend-evidence/qos-runtime-hfsc.png',
      fullPage: true,
    });
  } finally {
    vsctl('--if-exists', 'del-br', bridge);
    for (const uuid of tagged('QoS')) vsctl('destroy', 'QoS', uuid);
    for (const uuid of tagged('Queue')) vsctl('destroy', 'Queue', uuid);
    for (const device of interfaces) command('ip', 'link', 'del', device);
    for (const namespace of createdNS) command('ip', 'netns', 'del', namespace);
  }
});
