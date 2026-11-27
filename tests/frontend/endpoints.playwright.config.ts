import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import base from '../../frontend/playwright.config';

export default defineConfig({
  ...base,
  testDir: '.',
  testMatch: 'endpoints.execution.ts',
  outputDir: '../../test-results/ovs-witness/endpoints-frontend',
  reporter: [
    ['list'],
    [
      'junit',
      {
        outputFile: fileURLToPath(
          new URL(
            '../../test-results/ovs-witness/endpoints.xml',
            import.meta.url,
          ),
        ),
      },
    ],
  ],
});
