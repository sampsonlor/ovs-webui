import { mkdirSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const suite = process.argv[2];
if (!['unit', 'integration'].includes(suite))
  throw new Error('Choose unit or integration.');
const directory = resolve(suite === 'unit' ? 'tests' : 'tests/ci');
const files = readdirSync(directory)
  .filter((name) => name.endsWith('.test.mjs'))
  .sort()
  .map((name) => resolve(directory, name));
if (!files.length)
  throw new Error('No tests found; refusing an empty success.');
mkdirSync('test-results', { recursive: true });
const result = spawnSync(
  process.execPath,
  [
    '--experimental-strip-types',
    '--test',
    '--test-reporter=spec',
    '--test-reporter=junit',
    '--test-reporter-destination=stdout',
    `--test-reporter-destination=test-results/${suite}.xml`,
    ...files,
  ],
  { stdio: 'inherit', windowsHide: true },
);
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
