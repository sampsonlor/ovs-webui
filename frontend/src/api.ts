import type {
  Problem,
  RequestReceipt,
  Session,
} from '../../clients/typescript/public-v1.generated';
import {
  receiptMatches,
  receiptURL,
} from '../../clients/typescript/recovery.ts';
import type { RecoveryIdentity } from '../../clients/typescript/recovery';

export class APIError extends Error {
  status: number;
  problem: Partial<Problem>;
  constructor(status: number, problem: Partial<Problem>) {
    super(problem.code ?? `HTTP_${status}`);
    this.status = status;
    this.problem = problem;
  }
}

export function requestID(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  let time = BigInt(Date.now());
  for (let i = 5; i >= 0; i--) {
    b[i] = Number(time & BigInt(255));
    time >>= BigInt(8);
  }
  b[6] = (b[6] & 15) | 0x70;
  b[8] = (b[8] & 63) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export type Pending = RecoveryIdentity & { principal_id: string; path: string };
export type StoragePort = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

// No credentials, CSRF tokens, form data or native configuration are persisted.
// Only a principal-bound command identity survives navigation and browser restart.
export class API {
  session: Session | null = null;
  private transport: typeof fetch;
  private storage: StoragePort;
  constructor(transport: typeof fetch, storage: StoragePort) {
    this.transport = transport;
    this.storage = storage;
  }
  private key(principal: string) {
    return `ovs.pending.v1.${principal}`;
  }
  pending(): Pending | null {
    if (!this.session) return null;
    const raw = this.storage.getItem(this.key(this.session.principal_id));
    if (!raw) return null;
    const p = JSON.parse(raw) as Pending;
    if (
      p.principal_id !== this.session.principal_id ||
      !/^[0-9a-f-]{36}$/.test(p.request_id) ||
      !/^[0-9a-f-]{36}$/.test(p.request_epoch) ||
      !['workspace', 'management'].includes(p.request_domain)
    ) {
      throw new Error('RECOVERY_IDENTITY_INVALID');
    }
    return p;
  }
  async read<T>(path: string): Promise<T> {
    return this.send<T>(path);
  }
  async send<T>(
    path: string,
    method = 'GET',
    body?: unknown,
    headers: Record<string, string> = {},
  ): Promise<T> {
    const response = await this.transport(`/api/v1${path}`, {
      method,
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      signal: AbortSignal.timeout(12000),
      headers: {
        Accept: 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(method !== 'GET' && this.session
          ? { 'X-OVS-CSRF-Token': this.session.csrf_token }
          : {}),
        ...headers,
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (response.status === 204) return undefined as T;
    if (!response.headers.get('Content-Type')?.includes('json'))
      throw new Error('INVALID_API_RESPONSE');
    const value: unknown = await response.json();
    if (!response.ok)
      throw new APIError(response.status, value as Partial<Problem>);
    return value as T;
  }
  async command<T>(
    path: string,
    method: 'POST' | 'PATCH',
    domain: 'workspace' | 'management',
    body: object,
    revision?: string,
  ): Promise<T> {
    return this.withRequestLock(() =>
      this.dispatch<T>(path, method, domain, body, revision),
    );
  }
  private async withRequestLock<T>(run: () => Promise<T>): Promise<T> {
    // A shared pending key alone is not a compare-and-swap across browser tabs.
    // Hold a same-origin Web Lock through persistence and acknowledgement.
    if (typeof window !== 'undefined') {
      if (!navigator.locks) throw new Error('CROSS_TAB_LOCK_UNAVAILABLE');
      return navigator.locks.request(
        `ovs.command.v1.${this.session?.principal_id ?? 'anonymous'}`,
        { ifAvailable: true },
        (lock) => {
          if (!lock) throw new Error('REQUEST_IN_PROGRESS_IN_ANOTHER_TAB');
          return run();
        },
      );
    }
    return run();
  }
  private forget(pending: Pending) {
    const key = this.key(pending.principal_id);
    // A delayed recovery must never remove a newer request's identity.
    const raw = this.storage.getItem(key);
    if (raw === JSON.stringify(pending)) this.storage.removeItem(key);
  }
  private async dispatch<T>(
    path: string,
    method: 'POST' | 'PATCH',
    domain: 'workspace' | 'management',
    body: object,
    revision?: string,
  ): Promise<T> {
    const session = this.session;
    if (!session) throw new Error('SESSION_REQUIRED');
    if (this.pending()) throw new Error('REQUEST_RECOVERY_REQUIRED');
    const pending: Pending = {
      principal_id: session.principal_id,
      path,
      request_id: requestID(),
      request_domain: domain,
      request_epoch: session.request_epochs[domain],
    };
    const key = this.key(session.principal_id);
    // A storage failure must stop dispatch, not turn a lost reply into a new POST.
    this.storage.setItem(key, JSON.stringify(pending));
    const result = await this.send<T>(
      path,
      method,
      { ...body, request_id: pending.request_id },
      {
        'Idempotency-Key': pending.request_id,
        'X-OVS-Request-Epoch': pending.request_epoch,
        ...(revision ? { 'If-Match': `"${revision}"` } : {}),
      },
    ).catch((error: unknown) => {
      if (
        error instanceof APIError &&
        ['none', 'not-started'].includes(error.problem.command_effect ?? '') &&
        error.problem.request_id === pending.request_id &&
        error.problem.request_domain === domain
      )
        this.forget(pending);
      throw error;
    });
    const data = result as Record<string, unknown> | undefined;
    const uuid = (value: unknown) =>
      typeof value === 'string' && /^[0-9a-f-]{14}4[0-9a-f-]{21}$/.test(value);
    if (
      !data ||
      (domain === 'management'
        ? data.request_id !== pending.request_id ||
          data.request_domain !== domain ||
          data.request_epoch !== pending.request_epoch ||
          !uuid(data.job_id) ||
          !this.resourceMatches(path, data.resource_ref)
        : !uuid(data.id) ||
          !uuid(data.revision) ||
          !Array.isArray(data.intents))
    ) {
      throw new Error('COMMAND_RESPONSE_IDENTITY_MISMATCH');
    }
    this.forget(pending);
    return result;
  }
  async recover(): Promise<RequestReceipt> {
    return this.withRequestLock(() => this.recoverOriginal());
  }
  private async recoverOriginal(): Promise<RequestReceipt> {
    const pending = this.pending();
    if (!pending) throw new Error('NO_PENDING_REQUEST');
    const receipt = await this.read<RequestReceipt>(
      receiptURL(pending).replace('/api/v1', ''),
    );
    if (!receiptMatches(receipt, pending))
      throw new Error('RECEIPT_IDENTITY_MISMATCH');
    // Unknown future receipt states cannot unlock another configuration command.
    if (['accepted', 'completed'].includes(receipt.state)) {
      if (!this.resourceMatches(pending.path, receipt.resource_ref))
        throw new Error('RECEIPT_RESOURCE_MISMATCH');
      this.forget(pending);
    }
    return receipt;
  }
  private resourceMatches(path: string, value: unknown): boolean {
    if (!value || typeof value !== 'object') return false;
    const ref = value as Record<string, unknown>;
    const kind =
      path === '/candidate'
        ? 'candidate'
        : path === '/validations'
          ? 'validation'
          : path.startsWith('/transactions')
            ? 'transaction'
            : '';
    return (
      !!kind &&
      ref.kind === kind &&
      typeof ref.id === 'string' &&
      /^[0-9a-f-]{14}4[0-9a-f-]{21}$/.test(ref.id)
    );
  }
}
