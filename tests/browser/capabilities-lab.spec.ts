import { test, expect } from './fixtures';

test('the persistence lab keeps native capability configuration disconnected in both information modes', async ({
  page,
}) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Editor A', exact: true }).click();
  await expect(
    page.getByRole('region', { name: 'Port inventory' }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Administration', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Capabilities', exact: true }),
  ).toBeVisible();
  for (const mode of ['standard', 'expert']) {
    await page.getByRole('button', { name: mode, exact: true }).click();
    await expect(
      page.getByText(
        'Native configuration is not connected to this persistence lab. Review this workflow in the synthetic prototype.',
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Review native change', exact: true }),
    ).toHaveCount(0);
  }
  await expect(
    page.getByRole('button', { name: 'Changes · 0', exact: true }),
  ).toBeVisible();
});
