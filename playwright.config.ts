import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests/browser',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  outputDir: 'test-results/browser',
  reporter: [['list'], ['junit', { outputFile: 'test-results/browser.xml' }]],
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: { width: 1440, height: 1000 },
    actionTimeout: 10_000,
    navigationTimeout: 30_000,
    serviceWorkers: 'block',
    screenshot: 'only-on-failure',
    trace: {
      mode: 'retain-on-failure',
      // DOM snapshots also collect network traffic, including lab session tokens.
      // Keep action/assertion history, accessible state and pixels only.
      snapshots: { dom: false, aria: true, screen: true },
      screenshots: true,
      sources: false,
      attachments: false,
    },
  },
});
