import { writable } from 'svelte/store';
import type {
  Accepted,
  Candidate,
  Login,
  Port,
  PortsPage,
  ResourceRef,
  Session,
  Transaction,
  Validation,
  VlanInput,
  Workspace,
} from '../../clients/typescript/public-v1.generated';
import { ResponseFence } from '../../clients/typescript/recovery.ts';
import { API, APIError } from './api.ts';
import type { Pending } from './api';
import { has } from './policy.ts';

export type Load<T> = {
  status: 'loading' | 'ready' | 'unavailable' | 'denied';
  value: T | null;
  error: string;
  received: number;
};
export const empty = <T>(): Load<T> => ({
  status: 'loading',
  value: null,
  error: '',
  received: 0,
});
type RecordValue = Record<string, unknown>;
export type Model = {
  session: Session | null;
  booting: boolean;
  sessionReady: boolean;
  busy: boolean;
  message: string;
  pending: Pending | null;
  path: string;
  ports: Load<PortsPage>;
  port: Load<Port>;
  workspace: Load<Workspace>;
  validation: Load<Validation>;
  transaction: Load<Transaction>;
  resource: Load<RecordValue>;
};
function initial(path: string): Model {
  return {
    session: null,
    booting: true,
    sessionReady: false,
    busy: false,
    message: '',
    pending: null,
    path,
    ports: empty(),
    port: empty(),
    workspace: empty(),
    validation: empty(),
    transaction: empty(),
    resource: empty(),
  };
}
export function refPath(ref: ResourceRef | null): string | null {
  if (!ref || !/^[0-9a-f-]{36}$/.test(ref.id)) return null;
  const base: Record<string, string> = {
    candidate: '/changes/candidates',
    validation: '/changes/validations',
    transaction: '/changes/transactions',
    job: '/operations/jobs',
    event: '/operations/events',
    audit: '/operations/audit',
    port: '/ports',
    bridge: '/bridges',
    interface: '/interfaces',
  };
  return base[ref.kind] ? `${base[ref.kind]}/${ref.id}` : null;
}
export function errorText(error: unknown): string {
  if (
    error instanceof APIError &&
    error.problem.command_effect === 'not-started' &&
    error.problem.code === 'TRANSACTION_VERSION_CHANGED'
  )
    return 'The transaction changed before this decision was accepted. Review the refreshed evidence and choose again.';
  return error instanceof Error ? error.message : 'UNKNOWN_ERROR';
}
export class Controller {
  state: Model;
  readonly store;
  private fence = new ResponseFence();
  private context = 0;
  private reading = false;
  private queued = false;
  readonly api: API;
  constructor(api: API, path = '/ports') {
    this.api = api;
    this.state = initial(path === '/' ? '/ports' : path);
    this.store = writable(this.state);
  }
  private update(value: Partial<Model>) {
    this.state = { ...this.state, ...value };
    this.store.set(this.state);
  }
  private reset(session: Session | null) {
    this.context++;
    this.fence.reset();
    this.api.session = session;
    this.state = {
      ...initial(this.state.path),
      session,
      booting: false,
      sessionReady: !!session,
    };
    this.update({ pending: this.api.pending() });
  }
  async login(value: Login) {
    if (this.state.busy) return;
    this.update({ busy: true, message: '' });
    try {
      const session = await this.api.send<Session>('/sessions', 'POST', value);
      this.reset(session);
      await this.refresh();
    } catch (e) {
      this.update({ message: errorText(e), busy: false, booting: false });
    }
  }
  async logout() {
    const apiSession = this.api.session;
    this.context++;
    this.fence.reset();
    this.update({ busy: true, sessionReady: false });
    try {
      await this.api.send('/session', 'DELETE');
      this.reset(null);
    } catch (e) {
      this.api.session = apiSession;
      this.update({
        message: `Sign-out was not acknowledged: ${errorText(e)}`,
        busy: false,
      });
    }
  }
  async elevate(password: string) {
    if (this.state.busy) return;
    const context = this.context;
    this.update({ busy: true, message: '' });
    try {
      const session = await this.api.send<Session>(
        '/session/reauthentication',
        'POST',
        { password },
      );
      if (context === this.context) {
        this.api.session = session;
        this.update({
          session,
          message:
            'Identity verified. Authorization is checked again for each command.',
        });
      }
    } catch (e) {
      if (context === this.context) this.update({ message: errorText(e) });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
  go(path: string) {
    this.fence.reset();
    this.update({
      path,
      port: empty(),
      validation: empty(),
      transaction: empty(),
      resource: empty(),
      message: '',
    });
    void this.refresh();
  }
  notify(message: string) {
    this.update({ message });
  }
  async refresh() {
    if (this.reading) {
      this.queued = true;
      return;
    }
    this.reading = true;
    const context = this.context;
    const ticket = this.fence.begin(this.state.path);
    const valid = () => context === this.context && this.fence.accepts(ticket);
    const path = this.state.path;
    try {
      const session = await this.api.read<Session>('/session');
      if (!valid()) return;
      const old = this.state.session;
      if (
        old &&
        (old.principal_id !== session.principal_id ||
          JSON.stringify(old.effective_capabilities) !==
            JSON.stringify(session.effective_capabilities))
      ) {
        this.reset(session);
        this.queued = true;
        return;
      }
      this.api.session = session;
      this.update({
        session,
        sessionReady: true,
        booting: false,
        pending: this.api.pending(),
      });
      const read = async <T>(
        url: string,
        capability?: string,
      ): Promise<Load<T>> => {
        if (capability && !has(session, capability))
          return {
            status: 'denied',
            value: null,
            error: 'CAPABILITY_DENIED',
            received: 0,
          };
        try {
          return {
            status: 'ready',
            value: await this.api.read<T>(url),
            error: '',
            received: performance.now(),
          };
        } catch (e) {
          // Any auth failure invalidates the whole read batch, including delayed
          // successes from requests that started before revocation.
          if (e instanceof APIError && (e.status === 401 || e.status === 403))
            throw e;
          return {
            status: 'unavailable',
            value: null,
            error: errorText(e),
            received: performance.now(),
          };
        }
      };
      const workspace = await read<Workspace>('/workspace', 'workspace.read');
      const candidate = workspace.value?.candidate;
      let validationID = workspace.value?.latest_validation?.id;
      let transactionID = workspace.value?.latest_transaction?.id;
      const parts = path.split('/').filter(Boolean);
      if (parts[1] === 'validations') validationID = parts[2];
      if (parts[1] === 'transactions') transactionID = parts[2];
      const tasks: Promise<void>[] = [];
      const changes: Partial<Model> = { workspace };
      if (path.startsWith('/ports')) {
        if (parts[1])
          tasks.push(
            read<Port>(`/ports/${parts[1]}`, 'inventory.read').then((port) => {
              changes.port = port;
            }),
          );
        else
          tasks.push(
            read<PortsPage>(
              '/ports' +
                (this.portCursor
                  ? `?cursor=${encodeURIComponent(this.portCursor)}`
                  : ''),
              'inventory.read',
            ).then((ports) => {
              changes.ports = ports;
            }),
          );
      }
      if (validationID && has(session, 'configuration.validate'))
        tasks.push(
          read<Validation>(
            `/validations/${validationID}`,
            'configuration.validate',
          ).then((validation) => {
            changes.validation = validation;
          }),
        );
      else changes.validation = empty();
      if (transactionID)
        tasks.push(
          read<Transaction>(
            `/transactions/${transactionID}`,
            'configuration.read',
          ).then((transaction) => {
            changes.transaction = transaction;
          }),
        );
      else changes.transaction = empty();
      let resourceURL = '';
      let capability = '';
      if (parts[0] === 'operations') {
        resourceURL = `/${parts.slice(1).join('/')}`;
        capability =
          (
            {
              jobs: 'jobs.read',
              events: 'events.read',
              audit: 'audit.read',
            } as Record<string, string>
          )[parts[1]] ?? 'unavailable';
      } else if (parts[0] === 'bridges' || parts[0] === 'interfaces') {
        resourceURL = path;
        capability = 'inventory.read';
      } else if (path === '/changes/transactions') {
        resourceURL = '/transactions';
        capability = 'configuration.read';
      } else if (path === '/overview') resourceURL = '/runtime';
      if (resourceURL)
        tasks.push(
          read<RecordValue>(resourceURL, capability).then((resource) => {
            changes.resource = resource;
          }),
        );
      await Promise.all(tasks);
      if (!valid()) return;
      this.update(changes);
      if (path === '/changes/candidate' && candidate)
        navigate(`/changes/candidates/${candidate.id}`, true);
    } catch (e) {
      if (!valid()) return;
      if (e instanceof APIError && e.status === 401) {
        this.reset(null);
        this.update({
          message:
            'Session expired or revoked. Sign in to resume server-owned resources.',
        });
      } else {
        // Clear protected values on failure rather than keeping revoked data in
        // page-local caches. Existing safety tasks continue on mgrd.
        this.update({
          booting: false,
          sessionReady: false,
          message: errorText(e),
          ports: empty(),
          port: empty(),
          workspace: empty(),
          validation: empty(),
          transaction: empty(),
          resource: empty(),
        });
      }
    } finally {
      this.reading = false;
      if (this.queued) {
        this.queued = false;
        void this.refresh();
      }
    }
  }
  private portCursor = '';
  page(cursor = '') {
    this.portCursor = cursor;
    this.update({ ports: empty() });
    void this.refresh();
  }
  async mutate(
    run: () => Promise<Candidate | Accepted>,
    after?: (value: Candidate | Accepted) => string | null,
  ) {
    if (this.state.busy || !this.state.sessionReady || this.state.pending)
      return;
    const context = this.context;
    this.update({ busy: true, message: '' });
    try {
      const result = await run();
      if (context !== this.context) return;
      this.update({
        pending: this.api.pending(),
        message: 'Request acknowledged. Reading authoritative state…',
      });
      const path = after?.(result);
      if (path) navigate(path);
    } catch (e) {
      if (context === this.context)
        this.update({ pending: this.api.pending(), message: errorText(e) });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
  stage(port: Port, value: VlanInput) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    const existing = c.intents.find(
      (i) =>
        i.operation === 'port.vlan.set' &&
        i.object.management_id === port.management_id,
    );
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          {
            operation: 'stage',
            intents: [
              {
                intent_id: existing?.intent_id ?? crypto.randomUUID(),
                operation: 'port.vlan.set',
                object: {
                  management_id: port.management_id,
                  ovs_uuid: port.ovs_uuid,
                  instance_generation: port.instance_generation,
                  table: 'Port',
                },
                value,
              },
            ],
          },
          c.revision,
        ),
      (v) => `/changes/candidates/${(v as Candidate).id}`,
    );
  }
  candidateCommand(body: object) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    return this.mutate(() =>
      this.api.command<Candidate>(
        '/candidate',
        'PATCH',
        'workspace',
        body,
        c.revision,
      ),
    );
  }
  validate() {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    return this.mutate(
      () =>
        this.api.command<Accepted>('/validations', 'POST', 'management', {
          candidate_id: c.id,
          candidate_revision: c.revision,
        }),
      (r) => refPath((r as Accepted).resource_ref),
    );
  }
  apply(reason: string) {
    const c = this.state.workspace.value?.candidate,
      v = this.state.validation.value;
    if (!c || !v) return;
    return this.mutate(
      () =>
        this.api.command<Accepted>('/transactions', 'POST', 'management', {
          candidate_id: c.id,
          candidate_revision: c.revision,
          validation_id: v.id,
          mode: 'safe-apply',
          reason,
        }),
      (r) => refPath((r as Accepted).resource_ref),
    );
  }
  decide(decision: 'confirm' | 'rollback') {
    const t = this.state.transaction.value;
    if (!t) return;
    return this.mutate(() =>
      this.api.command<Accepted>(
        `/transactions/${t.id}/decisions`,
        'POST',
        'management',
        { decision, expected_sequence: t.sequence },
      ),
    );
  }
  async recover() {
    if (this.state.busy || !this.state.sessionReady) return;
    const context = this.context;
    this.update({ busy: true });
    try {
      const receipt = await this.api.recover();
      if (context !== this.context) return;
      this.update({
        pending: this.api.pending(),
        message: `Original receipt: ${receipt.state}. No command was resubmitted.`,
      });
      const path = refPath(receipt.resource_ref);
      if (path) navigate(path);
    } catch (e) {
      if (context === this.context)
        this.update({
          message: `Original outcome remains unknown: ${errorText(e)}. Refresh evidence; do not submit another command.`,
        });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
}

// Browser construction is lazy so authority/recovery logic can be tested without
// DOM globals or mock production services.
export let controller: Controller;
export function start(): Controller {
  controller = new Controller(
    new API(fetch.bind(window), localStorage),
    location.pathname,
  );
  return controller;
}
export function navigate(path: string, replace = false) {
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  controller.go(path);
}
