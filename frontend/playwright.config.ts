import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '../tests/frontend',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 70000,
  expect: { timeout: 15000 },
  outputDir: '../test-results/frontend',
  reporter: [['list'], ['junit', { outputFile: 'test-results/frontend.xml' }]],
  use: {
    browserName: 'chromium',
    headless: true,
    viewport: { width: 1440, height: 1000 },
    ignoreHTTPSErrors: true,
    serviceWorkers: 'block',
    actionTimeout: 15000,
    navigationTimeout: 20000,
    // Real cookie/CSRF/password traffic must not be serialized into a trace.
    trace: 'off',
    video: 'off',
    screenshot: 'only-on-failure',
  },
});
