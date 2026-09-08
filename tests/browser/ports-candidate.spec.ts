import type { Page } from '@playwright/test';
import { test, expect } from './fixtures';

function recordRequestId(body: unknown, requests: Set<string>) {
  if (
    typeof body !== 'object' ||
    body === null ||
    !('requestId' in body) ||
    typeof body.requestId !== 'string'
  )
    throw new Error('Expected a typed command with its original request ID.');
  requests.add(body.requestId);
}

async function connect(page: Page, identity = 'Editor A') {
  await page.goto('/');
  await page.getByRole('button', { name: identity, exact: true }).click();
  await expect(
    page.getByRole('region', { name: 'Port inventory' }),
  ).toBeVisible();
  if (identity !== 'Read-only')
    await page
      .getByLabel('Local observation fixture')
      .selectOption('safety-available');
}

async function edit(page: Page) {
  await page
    .getByRole('button', { name: 'Inspect server-07', exact: true })
    .click();
  await page.getByRole('button', { name: 'Edit VLAN', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Edit VLAN · server-07', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('combobox', { name: 'VLAN mode', exact: true })
    .selectOption('trunk');
  await page.getByLabel('Allowed VLANs', { exact: true }).fill('120, 240');
}

async function stage(page: Page) {
  await edit(page);
  await page
    .getByRole('button', { name: 'Add to workspace', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Trunk · 120, 240', { exact: true }),
  ).toBeVisible();
}

async function changes(page: Page) {
  await page.getByRole('button', { name: /^Changes ·/ }).click();
  await expect(
    page.getByRole('heading', { name: 'Candidate workspace', exact: true }),
  ).toBeVisible();
}

async function validate(page: Page) {
  await page
    .getByRole('button', { name: /^(Run validation|Validate again)$/ })
    .click();
  await expect(
    page.getByText('Passed · synthetic checks', { exact: true }),
  ).toBeVisible();
}

test('Ports → VLAN → Candidate → Diff / Validation survives refresh in Standard and Expert', async ({
  page,
}) => {
  await connect(page);
  await stage(page);
  await page.reload();
  await expect(
    page.getByRole('region', { name: 'Port inventory' }),
  ).toBeVisible();
  // A saved Candidate must not have changed the running Port.
  await expect(
    page.getByRole('row').filter({ hasText: 'server-07' }),
  ).toContainText('Access · VLAN 120');
  await changes(page);
  await expect(
    page.getByText('Trunk · 120, 240', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Review diff', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Diff review', exact: true }),
  ).toBeVisible();
  await validate(page);
  await expect(page.getByText('OVS mapping', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(page.getByText('generation', { exact: true })).toBeVisible();
  await expect(
    page.getByText('Passed · synthetic checks', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'standard', exact: true }).click();
  await expect(page.getByText('generation', { exact: true })).toHaveCount(0);
  await page.reload();
  await changes(page);
  await expect(
    page.getByText('Passed · synthetic checks', { exact: true }),
  ).toBeVisible();
});

test('read-only identity remains unable to edit or validate in both modes', async ({
  page,
}) => {
  await connect(page, 'Read-only');
  await page
    .getByRole('button', { name: 'Inspect server-07', exact: true })
    .click();
  for (const mode of ['standard', 'expert']) {
    await page.getByRole('button', { name: mode, exact: true }).click();
    await expect(
      page.getByText('Read-only access', { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Edit VLAN', exact: true }),
    ).toBeDisabled();
  }
  await changes(page);
  await expect(
    page.getByRole('button', { name: 'Run validation', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Discard saved intent', exact: true }),
  ).toBeDisabled();
  await page.reload();
  await expect(
    page.getByText('Read-only access', { exact: true }),
  ).toBeVisible();
});

test('stale generation expires validation and requires a reviewed rebase', async ({
  page,
}) => {
  await connect(page);
  await stage(page);
  await validate(page);
  await page.getByLabel('Local observation fixture').selectOption('stale');
  await expect(
    page.getByText('Candidate base is stale', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Validation expired', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Validate again', exact: true }),
  ).toBeDisabled();
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Validate again', exact: true }),
  ).toBeDisabled();
  await page
    .getByRole('button', { name: 'Rebase non-overlapping change', exact: true })
    .click();
  await expect(
    page.getByText('Candidate base is stale', { exact: true }),
  ).toHaveCount(0);
  await validate(page);
});

test('VLAN conflict shows Base / Current / Mine and requires an explicit choice', async ({
  page,
}) => {
  await connect(page);
  await stage(page);
  await page.getByLabel('Local observation fixture').selectOption('conflict');
  await expect(
    page.getByText('The same VLAN field changed', { exact: true }),
  ).toBeVisible();
  for (const label of ['Base', 'Current system', 'Your change'])
    await expect(page.getByText(label, { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Run validation', exact: true }),
  ).toBeDisabled();
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Run validation', exact: true }),
  ).toBeDisabled();
  await page
    .getByRole('button', { name: 'Use my value in Candidate', exact: true })
    .click();
  await expect(
    page.getByText('The same VLAN field changed', { exact: true }),
  ).toHaveCount(0);
  await validate(page);
  await page.getByRole('button', { name: 'Browse Ports', exact: true }).click();
  await expect(
    page.getByRole('row').filter({ hasText: 'server-07' }),
  ).toContainText('Access · VLAN 130');
});

test('a lost save response is OutcomeUnknown and recovers the original request without a second write', async ({
  page,
  labServer,
}) => {
  await connect(page);
  await edit(page);
  const writes = new Set<string>();
  await page.route('**/api/v1/requests/**', (route) => route.abort('failed'));
  page.on('request', (request) => {
    if (
      request.method() === 'PATCH' &&
      new URL(request.url()).pathname === '/api/v1/candidate'
    )
      recordRequestId(request.postDataJSON(), writes);
  });
  await labServer.dropResponses('candidate');
  await page
    .getByRole('button', { name: 'Add to workspace', exact: true })
    .click();
  await expect(
    page.getByText('Candidate save outcome unknown', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Add to workspace', exact: true }),
  ).toBeDisabled();
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Add to workspace', exact: true }),
  ).toBeDisabled();
  await page.unroute('**/api/v1/requests/**');
  await page
    .getByRole('button', { name: 'Check original request', exact: true })
    .click();
  await expect(
    page.getByText('Candidate save outcome unknown', { exact: true }),
  ).toHaveCount(0);
  await changes(page);
  await expect(
    page.getByText('Trunk · 120, 240', { exact: true }),
  ).toBeVisible();
  expect(writes.size).toBe(1);
});

test('provider failure and revoked permission preserve evidence while blocking new actions', async ({
  page,
}) => {
  await connect(page);
  await stage(page);
  await validate(page);
  await page
    .getByLabel('Local observation fixture')
    .selectOption('provider-unavailable');
  await expect(
    page.getByText('Port provider unavailable', { exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Validate again', exact: true })
    .click();
  await expect(
    page.getByText('Inventory provider is unavailable.', { exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Review Safe Apply', exact: true })
    .click();
  await page
    .getByLabel('Reason for this action')
    .fill('Review provider failure');
  await expect(
    page.getByRole('button', { name: 'Apply safely', exact: true }),
  ).toBeDisabled();
  await page
    .getByRole('button', { name: 'Review diff / validation', exact: true })
    .click();
  await page.getByLabel('Local observation fixture').selectOption('healthy');
  await page
    .getByLabel('Local observation fixture')
    .selectOption('permission-revoked');
  await expect(
    page.getByText('Read-only access', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'expert', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Validate again', exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Discard saved intent', exact: true }),
  ).toBeDisabled();
});

async function prepareSafeApply(page: Page) {
  await connect(page);
  await stage(page);
  await validate(page);
  await page
    .getByRole('button', { name: 'Review Safe Apply', exact: true })
    .click();
  await page
    .getByLabel('Reason for this action')
    .fill('Synthetic browser recovery check');
}

test('OutcomeUnknown survives refresh and resumes the original Safe Apply without resubmission', async ({
  page,
  labServer,
}) => {
  await prepareSafeApply(page);
  const starts = new Set<string>();
  await page.route('**/api/v1/requests/**', (route) => route.abort('failed'));
  page.on('request', (request) => {
    if (
      request.method() === 'POST' &&
      new URL(request.url()).pathname === '/api/v1/transactions'
    )
      recordRequestId(request.postDataJSON(), starts);
  });
  await labServer.dropResponses('transaction');
  await page.getByRole('button', { name: 'Apply safely', exact: true }).click();
  await expect(
    page.getByText('Transaction request outcome unknown', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Apply safely', exact: true }),
  ).toBeDisabled();
  await page.reload();
  await expect(
    page.getByText('Transaction request outcome unknown', { exact: true }),
  ).toBeVisible();
  await page.unroute('**/api/v1/requests/**');
  await page
    .getByRole('button', { name: 'Check original request', exact: true })
    .click();
  await expect(
    page.getByText('Transaction request outcome unknown', { exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole('button', { name: 'Review server operation', exact: true })
    .click();
  await expect(
    page.getByText('Confirmation window', { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Reason for this action')
    .fill('Verify recovered synthetic transaction');
  await expect(
    page.getByRole('button', { name: 'Confirm connectivity', exact: true }),
  ).toBeEnabled();
  expect(starts.size).toBe(1);
});

test('Drift blocks confirmation and direct rollback while preserving external VLAN changes', async ({
  page,
}) => {
  await prepareSafeApply(page);
  await page.getByRole('button', { name: 'Apply safely', exact: true }).click();
  await expect(
    page.getByText('Confirmation window', { exact: true }),
  ).toBeVisible();
  await page.getByLabel('Local observation fixture').selectOption('conflict');
  await expect(
    page.getByText('Drift or safety degradation', { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Reason for this action')
    .fill('Review drift and compare before rollback');
  for (const mode of ['standard', 'expert']) {
    await page.getByRole('button', { name: mode, exact: true }).click();
    await expect(
      page.getByRole('button', { name: 'Confirm connectivity', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole('button', { name: 'Roll back', exact: true }),
    ).toBeDisabled();
  }
  await page
    .getByRole('button', { name: 'Check server evidence', exact: true })
    .click();
  await expect(
    page.getByText('Drift or safety degradation', { exact: true }),
  ).toBeVisible();
  await changes(page);
  await expect(
    page.getByText('Node operation unresolved', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Discard saved intent', exact: true }),
  ).toBeDisabled();
  await page.getByRole('button', { name: 'Browse Ports', exact: true }).click();
  await expect(
    page.getByRole('row').filter({ hasText: 'server-07' }),
  ).toContainText('Access · VLAN 130');
});

test('tablet and mobile can review saved intent without validation or discard actions', async ({
  page,
}) => {
  await connect(page);
  await stage(page);
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(
      page.getByRole('heading', { name: 'Candidate workspace', exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Run validation', exact: true }),
    ).toBeHidden();
    await expect(
      page.getByRole('button', { name: 'Discard saved intent', exact: true }),
    ).toBeHidden();
    await expect(
      page.getByText('Trunk · 120, 240', { exact: true }),
    ).toBeVisible();
  }
});
