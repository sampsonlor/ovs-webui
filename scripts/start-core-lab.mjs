import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const entry = fileURLToPath(new URL('./cli.js', import.meta.resolve('vinext')));
const child = spawn(
  process.execPath,
  [entry, 'dev', '--hostname', '127.0.0.1', ...process.argv.slice(2)],
  { stdio: 'inherit', env: { ...process.env, OVS_CORE_LAB: '1' } },
);
child.on('exit', (code) => {
  process.exitCode = code ?? 1;
});
for (const signal of ['SIGINT', 'SIGTERM'])
  process.on(signal, () => child.kill(signal));
