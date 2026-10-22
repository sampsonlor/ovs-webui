import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import base from './playwright.config';

export default defineConfig({
  ...base,
  testMatch: '**/spanning-tree.execution.ts',
  outputDir: '../test-results/ovs-witness/frontend',
  reporter: [['list'], ['junit', { outputFile: fileURLToPath(new URL('../test-results/ovs-witness/frontend.xml', import.meta.url)) }]],
});
