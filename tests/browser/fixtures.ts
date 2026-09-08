import { test as base, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { resolve, dirname, basename, join } from 'node:path';
import { tmpdir } from 'node:os';

async function removeDirectory(directory: string, root: string) {
  const target = resolve(directory);
  if (dirname(target) !== root || !basename(target).startsWith('ovs-browser-'))
    throw new Error(
      'Refusing cleanup outside the isolated browser test directory.',
    );
  await rm(target, { recursive: true, force: true, maxRetries: 5 });
}

type LabServer = {
  origin: string;
  reset: () => Promise<void>;
  dropResponses: (operation: 'candidate' | 'transaction') => Promise<void>;
};

export const test = base.extend<{ baseURL: string }, { labServer: LabServer }>({
  labServer: [
    async ({ browserName }, provide) => {
      const root = resolve(tmpdir());
      const directory = await mkdtemp(
        join(root, `ovs-browser-${browserName}-`),
      );
      const child = spawn(
        process.execPath,
        ['--experimental-strip-types', 'tests/browser/start-server.mjs'],
        {
          cwd: resolve('.'),
          windowsHide: true,
          stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
          env: {
            ...process.env,
            OVS_BROWSER_TEST: '1',
            OVS_BROWSER_DATA_DIR: directory,
          },
        },
      );
      let log = '';
      for (const stream of [child.stdout, child.stderr])
        stream?.on('data', (chunk: Buffer) => {
          log = (log + chunk.toString()).slice(-8000);
        });
      try {
        const origin = await new Promise<string>((resolveReady, reject) => {
          const timer = setTimeout(
            () =>
              finish(new Error(`Browser service startup timed out. ${log}`)),
            60_000,
          );
          const onError = (error: Error) => finish(error);
          const onExit = () =>
            finish(
              new Error(`Browser service exited before readiness. ${log}`),
            );
          const onMessage = (message: unknown) => {
            if (
              typeof message !== 'object' ||
              message === null ||
              !('origin' in message) ||
              !('ready' in message)
            )
              return;
            if (
              message.ready === true &&
              typeof message.origin === 'string' &&
              /^http:\/\/127\.0\.0\.1:\d+$/.test(message.origin)
            )
              finish(null, message.origin);
          };
          function finish(error: Error | null, value?: string) {
            clearTimeout(timer);
            child.off('error', onError);
            child.off('exit', onExit);
            child.off('message', onMessage);
            if (error) reject(error);
            else resolveReady(value!);
          }
          child.once('error', onError);
          child.once('exit', onExit);
          child.on('message', onMessage);
        });
        async function command(instruction: string) {
          await new Promise<void>((resolveReset, reject) => {
            const timer = setTimeout(
              () => finish(new Error('Isolated service command timed out.')),
              10_000,
            );
            const onMessage = (message: unknown) => {
              if (
                typeof message === 'object' &&
                message !== null &&
                'ack' in message &&
                message.ack === instruction
              )
                finish(null);
            };
            function finish(error: Error | null) {
              clearTimeout(timer);
              child.off('message', onMessage);
              if (error) reject(error);
              else resolveReset();
            }
            child.on('message', onMessage);
            child.send(instruction, (error) => {
              if (error) finish(error);
            });
          });
        }
        await provide({
          origin,
          reset: () => command('reset'),
          dropResponses: (operation) => command(`drop-${operation}-response`),
        });
      } finally {
        if (child.exitCode === null && child.signalCode === null) {
          const exited = once(child, 'exit');
          if (child.connected) child.send('stop');
          else child.kill();
          const force = setTimeout(() => child.kill('SIGKILL'), 8000);
          try {
            await exited;
          } finally {
            clearTimeout(force);
          }
        }
        await removeDirectory(directory, root);
      }
    },
    { scope: 'worker', timeout: 90_000 },
  ],
  baseURL: async ({ labServer }, provide) => {
    await labServer.reset();
    await provide(labServer.origin);
  },
});

export { expect };
