export type InterfaceScope = {
  bridgeID: string;
  nativeType: string | null;
  linkState: string;
};

export const nativeTypes = [
  'system',
  'internal',
  'patch',
  'vxlan',
  'geneve',
  'gre',
  'ip6gre',
  'erspan',
  'ip6erspan',
  'gtpu',
  'lisp',
  'stt',
  'dpdk',
  'dpdkvhostuser',
  'dpdkvhostuserclient',
  'dummy',
];

export function readInterfaceQuery(query: string) {
  const q = new URLSearchParams(query);
  return {
    interfaceFilter: q.get('filter') ?? '',
    interfaceLimit: q.has('limit') ? Number(q.get('limit')) : 25,
    interfaceBridgeID: q.get('bridge_id') ?? '',
    // An empty native type is an observed system default, not "all" or Unknown.
    interfaceNativeType: q.has('native_type') ? q.get('native_type')! : null,
    interfaceLinkState: q.get('link_state') ?? '',
  };
}

export function interfaceListPath(
  filter: string,
  limit: number,
  cursor: string,
  scope: InterfaceScope,
): string {
  const q = new URLSearchParams({ limit: String(limit) });
  if (filter) q.set('filter', filter);
  if (scope.bridgeID) q.set('bridge_id', scope.bridgeID);
  if (scope.nativeType !== null) q.set('native_type', scope.nativeType);
  if (scope.linkState) q.set('link_state', scope.linkState);
  if (cursor) q.set('cursor', cursor);
  return `/interfaces?${q}`;
}
