import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { CoreLabStore, labUsers } from './core-lab-store.mjs';

const cookieName = 'ovs_lab_session';
const loopbackHosts = new Set(['localhost', '127.0.0.1', '[::1]']);
const readToken = (req) =>
  (req.headers.cookie ?? '')
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${cookieName}=`))
    ?.slice(cookieName.length + 1);
async function readBody(req) {
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > 65_536) throw new Error('Request too large.');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
function send(res, result) {
  res.writeHead(result.status, {
    'Content-Type':
      result.status >= 400 ? 'application/problem+json' : 'application/json',
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
    ...(result.etag ? { ETag: result.etag } : {}),
    ...result.headers,
  });
  res.end(JSON.stringify(result.body));
}

export function coreLabMiddleware(store) {
  return async (
    req,
    res,
    next = () => send(res, store.problem('NOT_FOUND', 404, 'Unknown route.')),
  ) => {
    if (!req.url?.startsWith('/api/v1/') && !req.url?.startsWith('/__ovs_lab/'))
      return next();
    let url;
    try {
      url = new URL(req.url, `http://${req.headers.host}`);
    } catch {
      return send(res, store.problem('FORBIDDEN', 403, 'Invalid local host.'));
    }
    const peer = req.socket.remoteAddress;
    if (
      !loopbackHosts.has(url.hostname) ||
      !['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(peer)
    )
      return send(
        res,
        store.problem(
          'FORBIDDEN',
          403,
          'The integration lab is loopback-only.',
        ),
      );
    const mutating = req.method !== 'GET' && req.method !== 'HEAD';
    if (
      (mutating && req.headers.origin !== url.origin) ||
      (req.headers.origin && req.headers.origin !== url.origin)
    )
      return send(
        res,
        store.problem(
          'FORBIDDEN',
          403,
          'Cross-origin lab requests are not accepted.',
        ),
      );
    if (req.headers['sec-fetch-site'] === 'cross-site')
      return send(
        res,
        store.problem(
          'FORBIDDEN',
          403,
          'Cross-site lab requests are not accepted.',
        ),
      );
    const token = readToken(req);
    const session = store.session(token);
    const requestId =
      typeof req.headers['idempotency-key'] === 'string' &&
      /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(req.headers['idempotency-key'])
        ? req.headers['idempotency-key']
        : undefined;
    try {
      if (url.pathname === '/__ovs_lab/session') {
        if (req.method === 'POST') {
          if (session && req.headers['x-csrf-token'] !== session.csrfToken)
            return send(
              res,
              store.problem(
                'FORBIDDEN',
                403,
                'Refresh the local session before switching user.',
              ),
            );
          const body = await readBody(req);
          if (
            !Object.hasOwn(labUsers, body.principal) ||
            Object.keys(body).length !== 1
          )
            return send(
              res,
              store.problem(
                'INVALID_INTENT',
                422,
                'Choose a predefined demonstration user.',
              ),
            );
          const loggedIn = store.login(body.principal, token);
          return send(res, {
            status: 200,
            body: loggedIn.session,
            headers: {
              'Set-Cookie': `${cookieName}=${loggedIn.token}; Path=/; HttpOnly; SameSite=Strict; Max-Age=28800`,
            },
          });
        }
        if (!session)
          return send(
            res,
            store.problem(
              'UNAUTHENTICATED',
              401,
              'Choose a local demonstration user.',
            ),
          );
        if (req.method === 'DELETE') {
          if (req.headers['x-csrf-token'] !== session.csrfToken)
            return send(
              res,
              store.problem('FORBIDDEN', 403, 'Refresh the local session.'),
            );
          store.logout(token);
          return send(res, {
            status: 200,
            body: { signedOut: true },
            headers: {
              'Set-Cookie': `${cookieName}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`,
            },
          });
        }
        if (req.method === 'GET')
          return send(res, { status: 200, body: session });
      }
      if (!session || req.headers['x-ovs-lab-epoch'] !== session.epoch)
        return send(
          res,
          store.problem(
            'UNAUTHENTICATED',
            401,
            'The session changed. Clear client data and reload the workspace.',
            requestId,
          ),
        );
      if (mutating && req.headers['x-csrf-token'] !== session.csrfToken)
        return send(
          res,
          store.problem(
            'FORBIDDEN',
            403,
            'The session token changed.',
            requestId,
          ),
        );
      if (req.method === 'POST' && url.pathname === '/__ovs_lab/observations') {
        if (!session.editable)
          return send(
            res,
            store.problem(
              'FORBIDDEN',
              403,
              'Read-only cannot change review fixtures.',
            ),
          );
        const body = await readBody(req);
        if (
          ![
            'stale',
            'conflict',
            'provider-unavailable',
            'node-blocked',
            'healthy',
          ].includes(body.scenario) ||
          Object.keys(body).length !== 1
        )
          return send(
            res,
            store.problem(
              'INVALID_INTENT',
              422,
              'Choose a predefined local observation.',
            ),
          );
        if (body.scenario === 'stale') store.externalChange(null, null);
        else if (body.scenario === 'conflict') {
          const intent = store.candidate(session.principal).intents[0];
          if (!intent)
            return send(
              res,
              store.problem(
                'INVALID_INTENT',
                422,
                'Stage a VLAN change before simulating a conflict.',
              ),
            );
          const current = store
            .meta('inventory')
            .items.find((item) => item.id === intent.portId)
            .configuration.value;
          store.externalChange(intent.portId, {
            mode: 'access',
            tag: current.tag === 130 ? 140 : 130,
            trunks: [],
          });
        } else if (body.scenario === 'healthy') {
          store.setMeta('providerAvailable', true);
          store.setMeta('nodeBlocked', false);
        } else
          store.setMeta(
            body.scenario === 'node-blocked'
              ? 'nodeBlocked'
              : 'providerAvailable',
            body.scenario === 'node-blocked',
          );
        return send(res, {
          status: 200,
          body: { scenario: body.scenario, synthetic: true },
        });
      }
      if (req.method === 'GET' && url.pathname === '/api/v1/workspace')
        return send(res, { status: 200, body: store.workspace(session) });
      if (req.method === 'GET' && url.pathname === '/api/v1/ports')
        return send(
          res,
          store.inventory(
            url.searchParams.get('cursor'),
            url.searchParams.get('search') ?? '',
          ),
        );
      if (req.method === 'GET' && url.pathname.startsWith('/api/v1/ports/')) {
        if (!store.meta('providerAvailable'))
          return send(
            res,
            store.problem(
              'PROVIDER_UNAVAILABLE',
              503,
              'Synthetic provider unavailable.',
            ),
          );
        const port = store
          .meta('inventory')
          .items.find(
            (item) =>
              item.id ===
              decodeURIComponent(url.pathname.slice('/api/v1/ports/'.length)),
          );
        return send(
          res,
          port
            ? { status: 200, body: port }
            : store.problem('NOT_FOUND', 404, 'Port not found.'),
        );
      }
      if (url.pathname === '/api/v1/candidate') {
        if (req.method === 'GET') {
          const snapshot = store.snapshot(session.principal);
          return send(res, {
            status: 200,
            body: snapshot.candidate,
            etag: snapshot.etag,
          });
        }
        if (req.method === 'PATCH')
          return send(
            res,
            store.mutate(
              session,
              await readBody(req),
              req.headers['if-match'],
              req.headers['idempotency-key'],
            ),
          );
      }
      if (req.method === 'GET' && url.pathname.startsWith('/api/v1/requests/'))
        return send(
          res,
          store.readRequest(
            session.principal,
            decodeURIComponent(url.pathname.slice('/api/v1/requests/'.length)),
          ),
        );
      // This development slice has no OVS provider, apply or watchdog implementation.
      return send(
        res,
        store.problem(
          'NOT_FOUND',
          404,
          'This operation is not connected in the persistence lab.',
        ),
      );
    } catch {
      const result = store.problem(
        'INTERNAL_ERROR',
        500,
        'Local service could not complete the request. Re-read request evidence before retrying a write.',
        requestId,
      );
      result.body.commandEffect = 'unknown';
      return send(res, result);
    }
  };
}

export function coreLabPlugin(path = resolve('.ovs-lab/state.sqlite')) {
  let store;
  return {
    name: 'ovs-core-local-persistence-lab',
    apply: 'serve',
    configureServer(server) {
      mkdirSync(dirname(path), { recursive: true });
      store = new CoreLabStore(path);
      server.middlewares.use(coreLabMiddleware(store));
      server.httpServer?.once('close', () => {
        store?.close();
        store = undefined;
      });
    },
  };
}
