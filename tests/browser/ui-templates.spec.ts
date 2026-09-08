import { test, expect } from './fixtures';

test('native labels, groups, links and status preserve template interaction', async ({
  page,
}, testInfo) => {
  await page.goto('/__ui-tests');
  await expect(
    page.getByRole('heading', { name: 'UI template regression fixtures' }),
  ).toBeVisible();
  for (const [label, control] of [
    ['Review name', 'Review name'],
    ['Field note', 'Field note'],
    ['Prefix', 'Grouped input'],
    ['Notes', 'Grouped textarea'],
  ]) {
    await page.getByText(label, { exact: true }).click();
    await expect(
      page.getByRole('textbox', { name: control, exact: true }),
    ).toBeFocused();
  }
  await page.getByRole('button', { name: 'Action', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('status', { name: 'Action result' })).toHaveText(
    'Addon action',
  );
  await page.getByRole('button', { name: 'Save example', exact: true }).click();
  await expect(page.getByRole('status', { name: 'Action result' })).toHaveText(
    'Saved',
  );
  await expect(
    page.getByRole('group', { name: 'Review actions' }),
  ).toBeVisible();
  await expect(
    page.getByRole('list', { name: 'Example items' }).getByRole('listitem'),
  ).toHaveCount(2);
  await expect(
    page.getByRole('status', { name: 'Loading example' }),
  ).toBeVisible();
  await expect(page.getByText('Templates', { exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await page.getByRole('link', { name: '2', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#page-2$/);
  await page.getByRole('textbox', { name: 'Example digits' }).fill('123456');
  await expect(
    page.getByRole('textbox', { name: 'Example digits' }),
  ).toHaveValue('123456');
  await expect(page.getByText('Frame count', { exact: true })).toHaveCount(2);
  await expect(page.getByText('24', { exact: true })).toBeVisible();
  for (const theme of ['light', 'dark']) {
    if (theme === 'dark')
      await page
        .getByRole('button', { name: 'Dark theme', exact: true })
        .click();
    await expect(
      page.getByRole('group', { name: 'Input example', exact: true }),
    ).toHaveCSS('height', '32px');
    await page.getByText('Prefix', { exact: true }).click();
    await expect(
      page.getByRole('textbox', { name: 'Grouped input', exact: true }),
    ).toBeFocused();
    if (process.env.OVS_BROWSER_CAPTURE === '1')
      await page.screenshot({
        path: testInfo.outputPath(`ui-templates-${theme}.png`),
        fullPage: true,
      });
  }
});

test('external stores update carousel controls and the mobile breakpoint', async ({
  page,
}) => {
  await page.goto('/__ui-tests');
  const previous = page.getByRole('button', { name: 'Previous slide' });
  const next = page.getByRole('button', { name: 'Next slide' });
  await expect(previous).toBeDisabled();
  await expect(next).toBeEnabled();
  await page.getByRole('region', { name: 'Example carousel' }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(previous).toBeEnabled();
  await next.click();
  await expect(next).toBeDisabled();
  await page.getByRole('button', { name: 'Toggle slide count' }).click();
  await expect(previous).toBeDisabled();
  await expect(next).toBeDisabled();
  for (const [width, device] of [
    [767, 'Mobile'],
    [768, 'Desktop'],
    [390, 'Mobile'],
    [1440, 'Desktop'],
  ] as const) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(page.getByRole('status', { name: 'Device class' })).toHaveText(
      device,
    );
  }
});

test('command and combobox wrappers keep their label and keyboard behavior', async ({
  page,
}) => {
  await page.goto('/__ui-tests');
  const command = page.getByRole('combobox', { name: 'Search examples' });
  await command.fill('Example');
  await command.press('ArrowDown');
  await command.press('Enter');
  await expect(page.getByRole('status', { name: 'Action result' })).toHaveText(
    'Command chosen',
  );
  const object = page.getByRole('combobox', { name: 'Example object' });
  await object.fill('Port');
  await object.press('ArrowDown');
  await object.press('Enter');
  await expect(object).toHaveValue('Port');
});
