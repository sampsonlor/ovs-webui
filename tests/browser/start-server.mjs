import { resolve, dirname, basename } from 'node:path';
import { tmpdir } from 'node:os';
import { createServer, loadConfigFromFile } from 'vite';
import { coreLabMiddleware } from '../../dev/core-lab-plugin.mjs';
import { CoreLabStore } from '../../dev/core-lab-store.mjs';
import { startValidationWorker } from '../../dev/core-lab-validation.mjs';
import { startTransactionWorker } from '../../dev/core-lab-transactions.mjs';

const directory = resolve(process.env.OVS_BROWSER_DATA_DIR ?? '.');
if (
  process.env.OVS_BROWSER_TEST !== '1' ||
  dirname(directory) !== resolve(tmpdir()) ||
  !basename(directory).startsWith('ovs-browser-')
)
  throw new Error(
    'Browser tests require an isolated ovs-browser-* temporary directory.',
  );

const mode = process.env.OVS_BROWSER_MODE ?? 'lab';
if (!['lab', 'prototype'].includes(mode))
  throw new Error('Unknown browser service mode.');
const lab = mode === 'lab';
process.env.OVS_CORE_LAB = lab ? '1' : '0';
const loaded = await loadConfigFromFile({ command: 'serve', mode: 'test' });
if (!loaded) throw new Error('Missing application Vite configuration.');
const plugins = loaded.config.plugins;
const labIndex = plugins.findIndex(
  (plugin) => plugin?.name === 'ovs-core-local-persistence-lab',
);
if (lab && labIndex < 0)
  throw new Error('The real persistence lab must be enabled.');
// Reuse the actual application configuration and service, with a fresh database
// for each test. The user's .ovs-lab database is never opened by this process.
let store;
let middleware;
let stopValidation = () => {};
let stopTransactions = () => {};
let databaseNumber = 0;
let dropResponse = null;
function closeStore() {
  stopValidation();
  stopTransactions();
  store?.close();
  store = undefined;
}
function resetStore() {
  closeStore();
  dropResponse = null;
  if (!lab) return;
  store = new CoreLabStore(
    resolve(directory, `state-${++databaseNumber}.sqlite`),
  );
  middleware = coreLabMiddleware(store);
  stopValidation = startValidationWorker(store);
  stopTransactions = startTransactionWorker(store);
}
resetStore();
if (lab)
  plugins[labIndex] = {
    name: 'isolated-core-persistence-lab',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const droppedSave =
          dropResponse === 'candidate' &&
          req.method === 'PATCH' &&
          req.url === '/api/v1/candidate';
        const droppedTransaction =
          dropResponse === 'transaction' &&
          req.method === 'POST' &&
          req.url === '/api/v1/transactions';
        if (droppedSave || droppedTransaction) {
          const end = res.end.bind(res);
          res.end = (...args) => {
            // The middleware and store finish the real command before its reply
            // is dropped. No authenticated request is replayed by the test runner.
            if (res.statusCode >= 200 && res.statusCode < 300) {
              res.destroy();
              return res;
            }
            return end(...args);
          };
        }
        return middleware(req, res, next);
      });
    },
  };
plugins.unshift({
  name: 'isolated-ui-component-review',
  configureServer(server) {
    server.middlewares.use('/__ui-tests', async (_req, res, next) => {
      try {
        const html = await server.transformIndexHtml(
          '/__ui-tests',
          '<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="ui-test-root"></div><script type="module" src="/tests/browser/ui-fixture.tsx"></script></body></html>',
        );
        res.writeHead(200, {
          'Content-Type': 'text/html',
          'Cache-Control': 'no-store',
        });
        res.end(html);
      } catch (error) {
        next(error);
      }
    });
  },
});
const server = await createServer({
  ...loaded.config,
  configFile: false,
  mode: 'test',
  clearScreen: false,
  server: {
    ...loaded.config.server,
    host: '127.0.0.1',
    port: 0,
    strictPort: true,
  },
});
await server.listen();
const address = server.httpServer.address();
if (!address || typeof address === 'string')
  throw new Error('Missing test server address.');
process.send?.({ ready: true, origin: `http://127.0.0.1:${address.port}` });

let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  await server.close();
  closeStore();
  process.disconnect?.();
}
process.on('message', (message) => {
  if (message === 'stop') void stop();
  // Only the parent test runner can reset a fixture through private IPC.
  // There is no reset HTTP endpoint and each reset creates a new database file.
  if (message === 'reset' && !stopping) {
    resetStore();
    process.send?.({ ack: message });
  }
  if (
    ['drop-candidate-response', 'drop-transaction-response'].includes(
      message,
    ) &&
    !stopping
  ) {
    dropResponse =
      message === 'drop-candidate-response' ? 'candidate' : 'transaction';
    process.send?.({ ack: message });
  }
});
process.on('SIGTERM', () => void stop());
process.on('SIGINT', () => void stop());
