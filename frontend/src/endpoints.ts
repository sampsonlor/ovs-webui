export const endpointsRoute = '/visibility/endpoints';

export function endpointsPath(query: string, cursor?: string | null): string {
  const params = new URLSearchParams(query);
  if (!params.has('limit')) params.set('limit', '25');
  if (cursor) params.set('cursor', cursor);
  else params.delete('cursor');
  return endpointsRoute + '?' + params;
}

const reasons: Record<string, string> = {
  ENDPOINT_COVERAGE_LIMITED: 'Source coverage is incomplete. This observation could not be confirmed.',
  OVSDB_RESYNCING: 'Waiting for current OVS inventory to verify endpoint observations.',
  ENDPOINT_STALE: 'This endpoint observation is stale.',
  INSTANCE_RECONCILIATION_REQUIRED: 'The managed OVS instance identity needs reconciliation.',
  INVALID_ENDPOINT_FILTER: 'Check the MAC, IP address, VLAN and exact Bridge or Port management IDs.',
  OVS_ASSOCIATION_STALE: 'Waiting for a fresh OVS observation to verify the managed scope.',
  VLAN_NOT_REPORTED: 'This source does not report a verified VLAN.',
  IP_NOT_REPORTED: 'This source does not report an IP address for this scope.',
  MAC_NOT_VERIFIED: 'This source does not report a verified MAC address for this scope.',
};

export function endpointReason(code: string): string {
  return reasons[code] ?? code;
}

export const endpointSources = {
  fdb: 'FDB',
  neighbor: 'ARP/NDP',
  lldp: 'LLDP',
} as const;
