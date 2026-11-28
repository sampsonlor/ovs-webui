import { expect, test, type Page, type Response } from '@playwright/test';
import { readFileSync } from 'node:fs';
import type { OpenFlowReadPage } from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_OPENFLOW_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated qualified OpenFlow fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  bridge: string;
  bridgeId: string;
  cookie: string;
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

const viewer = '/switching/openflow';
const api = '/api/v1';
const observations = `/openflow/bridges/${fixture.bridgeId}/snapshots`;
const observer = 'browser-fdb-observer';
const denied = 'browser-fdb-inventory-only';

async function login(page: Page, name: string) {
  await page.goto(fixture.origin + viewer);
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[name]);
  const reply = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === `${api}/sessions` &&
      response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
  await expect(
    page.getByRole('heading', { name: 'OpenFlow Viewer', exact: true }),
  ).toBeVisible();
}

async function readPage(response: Response): Promise<OpenFlowReadPage> {
  const problem = response.ok()
    ? undefined
    : (await response.json().catch(() => null))?.code;
  expect(
    response.status(),
    `Qualified OpenFlow read${typeof problem === 'string' && /^[A-Z_]+$/.test(problem) ? ` (${problem})` : ''}`,
  ).toBe(200);
  return response.json();
}

test('qualified OpenFlow observes exact native scope, exports its original snapshot and enforces browser authority', async ({
  page,
  browser,
}) => {
  const decimal = BigInt(fixture.cookie).toString(10);
  const deniedContext = await browser.newContext({
    ignoreHTTPSErrors: true,
    serviceWorkers: 'block',
  });
  try {
    const deniedPage = await deniedContext.newPage();
    const deniedReads: string[] = [];
    deniedPage.on('request', (request) => {
      const path = new URL(request.url()).pathname;
      if (path.startsWith(`${api}/openflow/`)) deniedReads.push(path);
    });
    await Promise.all([login(page, observer), login(deniedPage, denied)]);
    await expect(
      deniedPage.getByText(
        'Current permissions or connection do not allow OpenFlow observations.',
      ),
    ).toBeVisible();
    await expect(
      deniedPage.getByRole('button', { name: 'Observe OpenFlow', exact: true }),
    ).toHaveCount(0);
    const deniedCapture = await deniedContext.request.get(
      `${fixture.origin}${api}${observations}?table=7`,
    );
    expect(deniedCapture.status()).toBe(403);
    expect(await deniedCapture.json()).toMatchObject({
      code: 'CAPABILITY_DENIED',
    });

    const captures: string[] = [];
    const mutations: string[] = [];
    page.on('request', (request) => {
      const path = new URL(request.url());
      if (
        path.origin !== fixture.origin ||
        !path.pathname.startsWith(`${api}/`)
      )
        return;
      if (!['GET', 'HEAD'].includes(request.method()))
        mutations.push(path.pathname);
      if (path.pathname === `${api}${observations}`) captures.push(path.search);
    });
    const scope = page.getByRole('region', {
      name: 'Observation scope',
      exact: true,
    });
    await scope.getByLabel('Bridge name', { exact: true }).fill(fixture.bridge);
    await scope
      .getByRole('button', { name: 'Find Bridges', exact: true })
      .click();
    const bridge = scope.getByLabel('Bridge', { exact: true });
    await expect(
      bridge.locator(`option[value="${fixture.bridgeId}"]`),
    ).toHaveText(fixture.bridge);
    await expect(
      scope.getByRole('checkbox', { name: 'Include raw Flow fields', exact: true }),
    ).toHaveCount(0);
    await bridge.selectOption(fixture.bridgeId);
    await scope.getByLabel('Table', { exact: true }).fill('7');
    await scope
      .getByLabel('Cookie (optional decimal)', { exact: true })
      .fill(decimal);
    await scope.getByLabel('Maximum flow rows', { exact: true }).fill('5');
    await scope
      .getByRole('checkbox', {
        name: 'Include Group and Meter configuration',
        exact: true,
      })
      .check();
    expect(captures).toEqual([]);

    const pageReply = page.waitForResponse((reply) => {
      const url = new URL(reply.url());
      return (
        url.pathname.startsWith(`${api}/openflow/snapshots/`) &&
        url.searchParams.get('offset') === '0' &&
        url.searchParams.get('page_size') === '25' &&
        reply.request().method() === 'GET'
      );
    });
    const [response] = await Promise.all([
      page.waitForResponse(
        (reply) =>
          new URL(reply.url()).pathname === `${api}${observations}` &&
          reply.request().method() === 'GET',
      ),
      scope
        .getByRole('button', { name: 'Observe OpenFlow', exact: true })
        .click(),
    ]);
    expect(Object.fromEntries(new URL(response.url()).searchParams)).toEqual({
      table: '7',
      row_limit: '5',
      cookie: decimal,
      groups_meters: 'true',
    });
    const observed = await readPage(response);
    const { id } = observed.snapshot;
    expect(observed.snapshot).toMatchObject({
      bridge_id: fixture.bridgeId,
      retained_rows: 1,
      truncated: false,
    });
    expect(observed.evidence).toMatchObject({
      bridge_id: fixture.bridgeId,
      instance_generation: observed.snapshot.instance_generation,
      ownership: 'unknown',
      field_coverage: 'metadata-only',
      source: 'openflow-1.3-unix',
      query: {
        table: 7,
        limit: 5,
        cookie: fixture.cookie,
        include_groups_meters: true,
      },
      group_meter_statistics: 'unknown',
      groups: 'observed',
      group_rows: [{ group_id: 17 }],
      rows: [{ table: 7, priority: 405, cookie: fixture.cookie }],
    });
    const original = page.getByRole('region', {
      name: 'Original snapshot',
      exact: true,
    });
    const snapshotPath = `${api}/openflow/snapshots/${id}`;
    const retainedPage = await pageReply;
    expect(new URL(retainedPage.url()).pathname).toBe(snapshotPath);
    expect(await readPage(retainedPage)).toEqual(observed);
    await expect(page).toHaveURL(
      `${fixture.origin}${viewer}?snapshot=${id}&offset=0`,
    );
    await expect(
      original.getByText(
        'Match values, action arguments and rule ownership are unavailable in this observation.',
        { exact: false },
      ),
    ).toBeVisible();
    await expect(
      original.getByText(fixture.cookie, { exact: true }),
    ).toBeVisible();
    const rows = page.getByRole('region', {
      name: 'Retained OpenFlow rows',
      exact: true,
    });
    await expect(
      rows.getByRole('cell', { name: '405', exact: true }),
    ).toBeVisible();
    await expect(
      rows.getByRole('cell', { name: '406', exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByText('Group 17 · Type 0', { exact: true }),
    ).toBeVisible();

    const downloadButton = page.getByRole('button', {
      name: 'Download original snapshot as JSON',
      exact: true,
    });
    const [exportResponse, download] = await Promise.all([
      page.waitForResponse((reply) => {
        const url = new URL(reply.url());
        return (
          url.pathname === snapshotPath &&
          url.searchParams.get('export') === 'true' &&
          reply.request().method() === 'GET'
        );
      }),
      page.waitForEvent('download', { timeout: 15_000 }),
      downloadButton.click(),
    ]);
    expect(await readPage(exportResponse)).toEqual(observed);
    expect(download.suggestedFilename()).toBe(`ovs-webui-openflow-${id}.json`);
    expect(JSON.parse(readFileSync((await download.path())!, 'utf8'))).toEqual(
      observed,
    );

    await deniedPage.goto(`${fixture.origin}${viewer}?snapshot=${id}&offset=0`);
    await expect(
      deniedPage.getByText(
        'Current permissions or connection do not allow OpenFlow observations.',
      ),
    ).toBeVisible();
    await expect(
      deniedPage.getByRole('button', {
        name: 'Download original snapshot as JSON',
        exact: true,
      }),
    ).toHaveCount(0);
    const deniedExport = await deniedContext.request.get(
      `${fixture.origin}${snapshotPath}?export=true`,
    );
    expect(deniedExport.status()).toBe(403);
    expect(await deniedExport.json()).toMatchObject({
      code: 'CAPABILITY_DENIED',
    });
    expect(deniedReads).toEqual([]);
    expect(captures).toHaveLength(1);
    expect(mutations).toEqual([]);
  } finally {
    await deniedContext.close();
  }
});
