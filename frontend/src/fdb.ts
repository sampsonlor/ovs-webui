export const fdbRoute = '/visibility/fdb';

export function fdbPath(query: string, cursor?: string | null): string {
  const params = new URLSearchParams(query);
  if (!params.has('limit')) params.set('limit', '25');
  if (cursor) params.set('cursor', cursor);
  else params.delete('cursor');
  return fdbRoute + '?' + params;
}

const reasons: Record<string, string> = {
  disabled: 'Native FDB observation is disabled.',
  unsupported: 'Native FDB observation is unsupported by the current source.',
  not_ready: 'Waiting for a current native FDB observation.',
  timeout: 'The native FDB observation timed out.',
  permission: 'Current authorization does not permit native FDB observation.',
  source_unavailable: 'The native FDB source is unavailable.',
  source_invalid: 'The native FDB source could not be verified.',
  no_managed_bridges: 'No managed Bridges could be verified for this FDB scope.',
  partial: 'Some Bridges or native FDB entries could not be observed. This table has partial coverage.',
  truncated: 'The native FDB collection reached its entry limit. Some entries are omitted from this snapshot.',
  expired: 'The native FDB observation has expired. Refresh to read the current Bridge scope.',
  epoch_changed: 'The native FDB source changed. Waiting for a new observation.',
};

export function fdbReason(code: string): string {
  const key = code.startsWith('FDB_') ? code.slice(4).toLowerCase() : code;
  return reasons[key] ?? 'Current FDB coverage is limited for this scope.';
}

export function fdbPortResolution(value: string | undefined): string {
  if (value === 'unmapped') return 'Unmapped native port';
  if (value === 'ambiguous') return 'Ambiguous native port';
  return 'Managed Port unavailable';
}
