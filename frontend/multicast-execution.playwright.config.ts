import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import base from './playwright.config';

export default defineConfig({
  ...base,
  testMatch: '**/multicast.execution.ts',
  outputDir: '../test-results/ovs-witness/multicast-frontend',
  reporter: [
    ['list'],
    [
      'junit',
      {
        outputFile: fileURLToPath(
          new URL(
            '../test-results/ovs-witness/multicast-frontend.xml',
            import.meta.url,
          ),
        ),
      },
    ],
  ],
});
