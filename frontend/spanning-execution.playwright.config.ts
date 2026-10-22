import { defineConfig } from '@playwright/test';
import base from './playwright.config';

export default defineConfig({
  ...base,
  testMatch: '**/spanning-tree.execution.ts',
  outputDir: '../test-results/ovs-witness/frontend',
  reporter: [['list'], ['junit', { outputFile: 'test-results/ovs-witness/frontend.xml' }]],
});
