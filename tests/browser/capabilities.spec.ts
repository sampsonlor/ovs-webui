import type { Page, TestInfo } from '@playwright/test';
import { test, expect } from './fixtures';

test.use({ serverMode: 'prototype' });

async function open(page: Page) {
  await page.goto('/');
  // Wait for the first client observation before using server-rendered controls.
  await expect(
    page.getByRole('button', {
      name: 'Navigation capability states',
      exact: true,
    }),
  ).toContainText('Available · 1');
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Capabilities', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toBeVisible();
}

async function sample(page: Page, value: string) {
  const select = page.getByLabel('Capability review sample', { exact: true });
  if (!(await select.isVisible()))
    await page
      .getByText('Synthetic capability review samples', { exact: true })
      .click();
  await select.selectOption(value);
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeEnabled();
}

async function stage(page: Page) {
  const review = page.getByRole('button', {
    name: 'Review native change',
    exact: true,
  });
  await review.focus();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('region', { name: 'Native change review', exact: true }),
  ).toBeVisible();
  const submit = page.getByRole('button', {
    name: 'Add native change to Candidate',
    exact: true,
  });
  await expect(submit).toBeDisabled();
  await page
    .getByRole('checkbox', {
      name: 'I reviewed the protected-peer traffic impact.',
      exact: true,
    })
    .check();
  await submit.click();
  await expect(
    page.getByRole('heading', { name: 'Candidate workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('protected: false', { exact: false }).first(),
  ).toBeVisible();
}

async function apply(page: Page) {
  await stage(page);
  await page
    .getByRole('button', { name: 'Review diff & validation', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Validate candidate', exact: true })
    .click();
  await page
    .getByRole('textbox', { name: /^Change reason/ })
    .fill('Isolate protected demo peers');
  await page
    .getByRole('button', { name: 'Review & apply', exact: true })
    .click();
  await page.getByRole('button', { name: 'Apply safely', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeEnabled();
}

async function capture(page: Page, testInfo: TestInfo, name: string) {
  if (process.env.OVS_BROWSER_CAPTURE === '1')
    await page.screenshot({
      path: testInfo.outputPath(`${name}.png`),
      fullPage: true,
    });
}

test('native capability uses the shared Candidate, validation, Safe Apply and Audit path', async ({
  page,
}, testInfo) => {
  await open(page);
  const evidence = page.getByRole('region', {
    name: 'Capability evidence',
    exact: true,
  });
  await expect(
    evidence.getByRole('status', { name: 'Selected capability availability' }),
  ).toHaveText('Available');
  await expect(
    evidence.getByText('Required permission', { exact: true }),
  ).toHaveCount(0);
  await capture(page, testInfo, 'capabilities-standard');
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(
    evidence.getByText('port.isolation.manage', { exact: true }),
  ).toBeVisible();
  await expect(
    evidence.getByText('ovs.port.protected', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'standard', exact: true }).click();
  await apply(page);
  await capture(page, testInfo, 'capabilities-safe-apply');
  await page
    .getByRole('button', { name: 'Confirm configuration', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await expect(evidence.getByText('Enabled', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeEnabled();
  await expect(evidence.getByText('Enabled', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Changes · 0', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Workspace is clean', exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Operations', exact: true }).click();
  await page
    .getByRole('button', { name: 'Events / Audit', exact: true })
    .click();
  await expect(
    page.getByText(
      'Safe Apply confirmed in the prototype. Candidate committed and workspace cleared.',
      { exact: true },
    ),
  ).toBeVisible();
});

for (const { group, cases } of [
  {
    group: 'support and authority',
    cases: ['missing', 'unsupported', 'unknown', 'external'],
  },
  {
    group: 'authorization and recovery',
    cases: ['unauthorized', 'unsafe', 'management-path', 'reauth-required'],
  },
  {
    group: 'provider and freshness',
    cases: ['degraded', 'unavailable', 'stale', 'generation-mismatch'],
  },
])
  test(`native ${group} exceptions block configuration in both information modes`, async ({
    page,
  }, testInfo) => {
    await open(page);
    for (const value of cases) {
      await sample(page, value);
      for (const mode of ['standard', 'expert']) {
        await page.getByRole('button', { name: mode, exact: true }).click();
        await expect(
          page.getByRole('button', {
            name: 'Review native change',
            exact: true,
          }),
        ).toHaveCount(0);
        await expect(
          page
            .getByRole('region', { name: 'Native action gates', exact: true })
            .getByText('Blocked', { exact: true })
            .first(),
        ).toBeVisible();
      }
    }
    await capture(page, testInfo, 'capabilities-expert-exception');
    await sample(page, 'normal');
    await expect(
      page.getByRole('button', { name: 'Review native change', exact: true }),
    ).toBeVisible();
  });

test('empty, failed, permission denied and filtered capability results are distinct and recover through a fresh read', async ({
  page,
}) => {
  await open(page);
  await page
    .getByRole('textbox', { name: 'Search capabilities', exact: true })
    .fill('nothing-matches');
  await expect(
    page.getByRole('heading', {
      name: 'No matching capabilities',
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Clear filters', exact: true })
    .click();
  await expect(
    page
      .getByRole('button', { name: /^Inspect / })
      .filter({ hasText: 'Manage' }),
  ).not.toHaveCount(0);
  await sample(page, 'empty');
  await expect(
    page
      .getByRole('status')
      .getByText('Registry returned no observations', { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole('region', { name: 'Capability evidence', exact: true })
      .getByText('Unknown', { exact: true })
      .first(),
  ).toBeVisible();
  await sample(page, 'failed');
  await expect(
    page.getByText('Registry evidence unavailable', { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Review state', { exact: true })
    .selectOption('permission-denied');
  await expect(
    page.getByRole('region', { name: 'Capability matrix', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole('region', { name: 'Capability evidence', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole('button', {
      name: 'Navigation capability states',
      exact: true,
    }),
  ).toContainText('Access denied');
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeDisabled();
  await page.getByLabel('Review state', { exact: true }).selectOption('normal');
  await sample(page, 'normal');
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toBeVisible();
});

test('withdrawing native permission during Safe Apply disables confirm and manual rollback without releasing the Job', async ({
  page,
}) => {
  await open(page);
  await apply(page);
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await sample(page, 'unauthorized');
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Inspect existing Safe Apply', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Roll back now', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByText('Native evidence requires review', { exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Inspect capability evidence', exact: true })
    .click();
  await sample(page, 'normal');
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Inspect existing Safe Apply', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Roll back now', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await expect(
    page
      .getByRole('region', { name: 'Capability evidence', exact: true })
      .getByRole('status', { name: 'Selected capability availability' }),
  ).toHaveText('Available');
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: 'Changes · 1', exact: true }),
  ).toBeVisible();
});

test('a pending registry read cannot restore revoked evidence or finish collection on mobile', async ({
  page,
}) => {
  await page.clock.install();
  await open(page);
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000));
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Reading registry…', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel('Review state', { exact: true })
    .selectOption('permission-denied');
  await page.clock.runFor(1000);
  await expect(
    page.getByRole('region', { name: 'Capability evidence', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole('button', {
      name: 'Navigation capability states',
      exact: true,
    }),
  ).toContainText('Access denied');
  await page.getByLabel('Review state', { exact: true }).selectOption('normal');
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.clock.runFor(1000);
  await expect(
    page.getByRole('heading', {
      name: 'Capability incident context',
      exact: true,
    }),
  ).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await expect(
    page
      .getByText(
        'Registry collection stopped on mobile. Retained observations require a larger review surface.',
        { exact: false },
      )
      .first(),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await page.clock.runFor(1000);
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toBeVisible();
});

test('Navigation, Overview, Health and the matrix share the same five capability counts', async ({
  page,
}) => {
  await open(page);
  await sample(page, 'missing');
  const counts = await page
    .getByRole('region', { name: 'Matrix capability states', exact: true })
    .getByRole('listitem')
    .allTextContents();
  expect(counts).toHaveLength(5);
  const navigation = page.getByRole('button', {
    name: 'Navigation capability states',
    exact: true,
  });
  for (const value of counts) await expect(navigation).toContainText(value);
  await page.getByRole('button', { name: 'Overview', exact: true }).click();
  const overview = page.getByRole('region', {
    name: 'Overview capability states',
    exact: true,
  });
  for (const value of counts) await expect(overview).toContainText(value);
  await page.getByRole('button', { name: 'Operations', exact: true }).click();
  const health = page.getByRole('region', {
    name: 'Health capability states',
    exact: true,
  });
  for (const value of counts) await expect(health).toContainText(value);
  await health
    .getByRole('button', { name: 'Inspect capabilities', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Capabilities', exact: true }),
  ).toBeVisible();
});

test('DPDK and offload remain observation-only and link to the same independently captured source', async ({
  page,
}) => {
  await open(page);
  await page
    .getByRole('button', { name: 'Refresh acceleration evidence', exact: true })
    .click();
  await page
    .getByRole('button', { name: 'Inspect DPDK runtime', exact: true })
    .click();
  const evidence = page.getByRole('region', {
    name: 'Capability evidence',
    exact: true,
  });
  await expect(evidence.getByText('Enabled', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  const source = await evidence
    .locator('div')
    .filter({ has: page.getByText('Evidence reference', { exact: true }) })
    .last()
    .getByRole('definition')
    .textContent();
  await page
    .getByRole('button', { name: 'Refresh registry', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeEnabled();
  await expect(evidence.getByText(source!, { exact: true })).toBeVisible();
  await page
    .getByRole('button', { name: 'Open related view', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'DPDK / Offload', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', {
      name: 'Open global capability evidence',
      exact: true,
    })
    .click();
  await expect(
    evidence.getByRole('heading', { name: 'DPDK runtime', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Inspect Hardware offload', exact: true })
    .click();
  await expect(evidence.getByText('Observe', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Native action gates', exact: true }),
  ).toHaveCount(0);
});

test('tablet and mobile retain review responsibilities and a pending Safe Apply; dark and enlarged text stay readable', async ({
  page,
}, testInfo) => {
  await open(page);
  await page.evaluate(() => document.documentElement.classList.add('dark'));
  await capture(page, testInfo, 'capabilities-dark');
  await page.evaluate(() => {
    document.documentElement.classList.remove('dark');
    document.documentElement.style.fontSize = '200%';
  });
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await capture(page, testInfo, 'capabilities-enlarged');
  await page.evaluate(() => {
    document.documentElement.style.fontSize = '';
  });
  await apply(page);
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await page.setViewportSize({ width: 820, height: 1180 });
  await expect(
    page.getByRole('region', { name: 'Capability matrix', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeVisible();
  await capture(page, testInfo, 'capabilities-tablet');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole('heading', {
      name: 'Capability incident context',
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Refresh registry', exact: true }),
  ).toBeHidden();
  await expect(
    page.getByRole('button', { name: 'Review native change', exact: true }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await capture(page, testInfo, 'capabilities-mobile');
  await page
    .getByRole('button', { name: 'Review existing Safe Apply', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeEnabled();
  await expect(
    page.getByRole('button', { name: 'Roll back now', exact: true }),
  ).toBeEnabled();
});
