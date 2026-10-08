// Test-only metadata. Never serialize a request, response, credential, header,
// raw URL, account, identifier, error message or successful response body.
const codes = new Set([
  'UNAUTHENTICATED',
  'CREDENTIAL_EXPIRED_OR_REVOKED',
  'SESSION_INVALID',
  'AUTH_UNAVAILABLE',
  'AUTH_CLOCK_UNSAFE',
  'AUTH_POLICY_INVALID',
  'AUTH_RESPONSE_INVALID',
  'AUTH_PROVIDER_UNAVAILABLE',
  'AUTH_RATE_LIMITED',
  'AUTH_GRANT_CAPACITY_REACHED',
  'AUTHENTICATION_FAILED',
  'CAPABILITY_DENIED',
  'IPC_QUEUE_FULL',
  'IPC_DEADLINE_EXCEEDED',
  'REQUEST_INTERRUPTED',
  'CSRF_REQUIRED',
  'CSRF_INVALID',
  'AMBIGUOUS_AUTHENTICATION',
]);
type Source = 'browser' | 'helper';
type Entry = {
  sequence: number;
  source: Source;
  operation:
    | 'create_session'
    | 'read_session'
    | 'read_workspace'
    | 'read_ports';
  outcome: 'pending' | 'response' | 'network_error';
  status: number | null;
  code: string | null;
};

export class AuthDiagnostics {
  private entries = new Map<number, Entry>();
  private observed = 0;
  private dropped = 0;
  private origin: string;
  constructor(origin: string) {
    this.origin = new URL(origin).origin;
  }
  begin(url: string, method: string, source: Source): number | null {
    if (source !== 'browser' && source !== 'helper') return null;
    let value: URL;
    try {
      value = new URL(url, this.origin);
    } catch {
      return null;
    }
    if (value.origin !== this.origin) return null;
    const reads: Record<string, Entry['operation']> = {
      '/api/v1/session': 'read_session',
      '/api/v1/workspace': 'read_workspace',
      '/api/v1/ports': 'read_ports',
    };
    const operation =
      method === 'POST' && value.pathname === '/api/v1/sessions'
        ? 'create_session'
        : method === 'GET'
          ? reads[value.pathname]
          : undefined;
    if (!operation) return null;
    if (this.entries.size >= 64) {
      this.entries.delete(this.entries.keys().next().value!);
      this.dropped++;
    }
    const index = ++this.observed;
    this.entries.set(index, {
      sequence: index,
      source,
      operation,
      outcome: 'pending',
      status: null,
      code: null,
    });
    return index;
  }
  response(index: number | null, status: number, problemCode?: unknown) {
    const entry = index === null ? undefined : this.entries.get(index);
    if (!entry || entry.outcome !== 'pending') return;
    if (!Number.isInteger(status) || status < 100 || status > 599) return;
    Object.assign(entry, {
      outcome: 'response',
      status,
      code:
        status < 400
          ? null
          : typeof problemCode === 'string' && codes.has(problemCode)
            ? problemCode
            : 'OTHER_ERROR',
    });
  }
  failed(index: number | null) {
    const entry = index === null ? undefined : this.entries.get(index);
    if (entry?.outcome === 'pending') entry.outcome = 'network_error';
  }
  snapshot() {
    return {
      version: 1,
      capacity: 64,
      observed: this.observed,
      dropped: this.dropped,
      entries: [...this.entries.values()].map((entry) => ({ ...entry })),
    };
  }
}
