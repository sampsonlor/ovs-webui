export const neighborsRoute = '/visibility/neighbors';

export const neighborScopes = [
  ['bridge_id', 'Bridge ID'],
  ['port_id', 'Port ID'],
  ['interface_id', 'Interface ID'],
] as const;

export function neighborsPath(query: string, cursor?: string | null): string {
  const params = new URLSearchParams(query);
  if (!params.has('limit')) params.set('limit', '25');
  if (cursor) params.set('cursor', cursor);
  else params.delete('cursor');
  return neighborsRoute + '?' + params;
}

const reasons: Record<string, string> = {
  NEIGHBORS_RESYNCING: 'Waiting for a current neighbor observation.',
  NEIGHBORS_EXPIRED: 'The retained neighbor observation has expired. Refresh to read the current table.',
  NEIGHBORS_SCOPE_CHANGED: 'The managed Interface scope changed. Waiting for a new neighbor observation.',
  NEIGHBORS_NO_PROVEN_SCOPE: 'No managed Linux Interface could be verified for this scope.',
  NEIGHBORS_PARTIAL_COVERAGE: 'Some eligible Interfaces could not be observed. This table has partial coverage.',
  NEIGHBORS_TRUNCATED: 'The neighbor collection reached its entry limit. Some entries are omitted from this snapshot.',
  NEIGHBORS_DUMP_TIMEOUT: 'The Linux neighbor table could not be read within the collection time limit.',
  NEIGHBORS_DUMP_UNAVAILABLE: 'The Linux neighbor table is unavailable.',
  NEIGHBORS_DUMP_UNPROVEN: 'The neighbor observation could not be verified against the current Interfaces.',
  NEIGHBORS_DUMP_BUDGET: 'The Linux neighbor collection exceeded its observation limit.',
  NEIGHBORS_INVALID_BINDINGS: 'The managed Interface identities could not be verified.',
  OVS_ASSOCIATION_STALE: 'Wait for a fresh OVS observation to verify the managed Interface scope.',
};

export function neighborsReason(code: string): string {
  return reasons[code] ?? 'Current neighbor coverage is limited for this scope.';
}

export function neighborFamily(family: string | undefined): string {
  return family === 'ipv4' ? 'IPv4 / ARP' : family === 'ipv6' ? 'IPv6 / NDP' : 'Unknown';
}
