import { createServer } from 'node:http';
import { resolve, dirname, basename, isAbsolute } from 'node:path';
import { tmpdir } from 'node:os';
import { CoreLabStore } from '../dev/core-lab-store.mjs';
import { coreLabMiddleware } from '../dev/core-lab-plugin.mjs';
import { startValidationWorker } from '../dev/core-lab-validation.mjs';

const directory = process.env.OVS_TEST_DATA_DIR;
if (
  process.env.OVS_TEST_SERVER !== '1' ||
  !directory ||
  !isAbsolute(directory) ||
  dirname(resolve(directory)) !== resolve(tmpdir()) ||
  !basename(directory).startsWith('ovs-ci-')
)
  throw new Error(
    'Test server requires its own ovs-ci-* directory directly under the system temporary root.',
  );

const store = new CoreLabStore(resolve(directory, 'state.sqlite'));
const stopWorker = process.argv.includes('--pause-validation')
  ? () => {}
  : startValidationWorker(store);
const server = createServer(coreLabMiddleware(store));
server.listen(0, '127.0.0.1', () => {
  const address = server.address();
  if (!address || typeof address === 'string')
    throw new Error('Missing local test address.');
  process.stdout.write(
    `${JSON.stringify({ ready: true, origin: `http://127.0.0.1:${address.port}` })}\n`,
  );
});
let stopping = false;
for (const signal of ['SIGINT', 'SIGTERM'])
  process.on(signal, () => {
    if (stopping) return;
    stopping = true;
    stopWorker();
    server.closeAllConnections();
    server.close(() => store.close());
  });
