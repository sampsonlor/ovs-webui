export const lldpRoute = '/visibility/lldp';

export function lldpPath(query: string, cursor?: string | null): string {
  const params = new URLSearchParams(query);
  if (!params.has('limit')) params.set('limit', '25');
  if (cursor) params.set('cursor', cursor);
  else params.delete('cursor');
  return lldpRoute + '?' + params;
}

const reasons: Record<string, string> = {
  LLDP_PROVIDER_DISABLED: 'Passive LLDP capture is disabled. An administrator can opt in managed Interfaces.',
  LLDP_CAPTURE_UNAVAILABLE: 'Passive LLDP capture is unavailable for this scope.',
  LLDP_CAPTURE_PARTIAL: 'Some configured Interfaces could not be observed. This capture has partial coverage.',
  LLDP_CAPTURE_TRUNCATED: 'Some advertisements could not be retained because the capture reached a resource limit.',
  LLDP_SCOPE_NOT_COVERED: 'No opted-in managed Interface could be verified for this capture scope.',
  LLDP_RESYNCING: 'Waiting for a current LLDP capture observation.',
  LLDP_EXPIRED: 'The LLDP capture observation has expired. Refresh to read current advertisements.',
  OVS_ASSOCIATION_STALE: 'Wait for a fresh OVS observation to verify the managed LLDP Interface scope.',
};

export function lldpReason(code: string): string {
  return reasons[code] ?? 'Current LLDP coverage is limited for this scope.';
}

export function lldpIdentifier(id: { encoding: string; value: string } | undefined): string {
  if (!id) return 'Unknown';
  return id.encoding === 'hex' ? 'Hex ' + id.value : id.value;
}
