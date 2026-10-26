import type {
  StaticTunnelObservation,
  StaticTunnelNativeOptions,
} from '../../clients/typescript/public-v1.generated';

export function tunnelOption(
  native:
    | Pick<
        StaticTunnelObservation,
        'configuration' | 'configuration_availability'
      >
    | null
    | undefined,
  key: keyof StaticTunnelNativeOptions,
): string {
  const options = native?.configuration?.options;
  if (options && Object.hasOwn(options, key)) {
    const value = options[key];
    return typeof value === 'string'
      ? value.trim()
        ? value
        : 'Empty native value'
      : 'Unknown';
  }
  return options &&
    native?.configuration_availability === 'known' &&
    key !== 'remote_ip'
    ? 'Native default (unset)'
    : 'Unknown';
}

export const tunnelOperations = [
  'tunnel.static.create',
  'tunnel.static.configure',
  'tunnel.static.delete',
] as const;
export function isTunnelOperation(operation?: string) {
  return (
    typeof operation === 'string' &&
    (tunnelOperations as readonly string[]).includes(operation)
  );
}
export function tunnelMessage(code?: string) {
  const messages: Record<string, string> = {
    CAPABILITY_DENIED:
      'Your current permissions allow review but do not allow tunnel changes.',
    TUNNEL_RELATION_UNKNOWN:
      'The current Bridge, Port and Interface association could not be established.',
    TUNNEL_DYNAMIC_READ_ONLY:
      'This tunnel uses flow-controlled endpoints or keys. Review it through its owning controller; this page keeps it read-only.',
    TUNNEL_ADVANCED_OPTIONS_READ_ONLY:
      'Additional native tunnel options are present. This combination remains read-only and its values are preserved.',
    TUNNEL_TYPE_UNSUPPORTED:
      'This native tunnel type is observable but is outside the supported static configuration workflow.',
    TUNNEL_SCHEMA_UNSUPPORTED:
      'This native schema does not provide the required tunnel configuration fields.',
    TUNNEL_SCHEMA_OR_RUNTIME_UNAVAILABLE:
      'Current schema and native type support could not be verified.',
    TUNNEL_RUNTIME_TYPE_UNAVAILABLE:
      'The current OVS daemon does not advertise this native tunnel type.',
    TUNNEL_AUTHORITY_OR_GRAPH_REQUIRED:
      'Local ownership or a supported Bridge and Port configuration is required before preparing a change.',
    TOPOLOGY_AUTHORITY_REQUIRED:
      'One of the affected objects has no current local management ownership.',
    TOPOLOGY_CREATION_GRANT_REQUIRED:
      'This new name is not currently available for local management on this Bridge.',
    TUNNEL_AUTHORITY_REVISION_CHANGED:
      'Local tunnel ownership changed. Discard this draft and review the current objects.',
    TUNNEL_DEPENDENCY_CHANGED:
      'The connected native configuration changed. Discard this draft and review the current Bridge, Ports and Interfaces.',
    TUNNEL_OBSERVATION_STALE:
      'This observation is not current. Wait for fresh native data before preparing a change.',
    TUNNEL_RECEIVE_IDENTITY_IN_USE:
      'Another tunnel already uses this receive endpoint and key. Review the existing tunnel first.',
    TUNNEL_EXISTING_RECEIVE_IDENTITY_UNPROVEN:
      'An existing dynamic or advanced tunnel may overlap this endpoint. Its receive identity cannot be safely excluded.',
    TUNNEL_STATIC_REMOTE_IP_REQUIRED:
      'Enter a canonical unicast remote IP address; host names and flow-controlled endpoints are not accepted.',
    TUNNEL_GRE_IPV4_REQUIRED:
      'The native GRE type in this workflow requires an IPv4 endpoint.',
    TUNNEL_LOCAL_IP_INVALID:
      'Review the local IP address. It must use the remote address family and differ from the remote endpoint.',
    TUNNEL_KEY_OUT_OF_RANGE:
      'The key must fit the native tunnel: 24 bits for VXLAN/Geneve or 32 bits for GRE.',
    TUNNEL_DESTINATION_PORT_INVALID:
      'A UDP destination port must be between 1 and 65535. Leave it empty for GRE.',
    HIGH_RISK_TOPOLOGY_SAFE_APPLY_REQUIRED:
      'This change requires Safe Apply, management reachability and explicit confirmation.',
    MANAGEMENT_PATH_AND_SOLE_UPLINK_REVIEW_REQUIRED:
      'Review the management path, endpoint routing and sole uplink before applying.',
    TOPOLOGY_CONFIGURATION_CHANGED:
      'Native configuration changed after capture. Discard this draft and review the current objects.',
    TOPOLOGY_ROOT_POLICY_CHANGED:
      'The shared native configuration changed. Review the current topology before restaging.',
    GENERATION_RECONCILIATION_REQUIRED:
      'The OVS instance changed. Review current object identities before preparing a new change.',
  };
  return (
    messages[code ?? ''] ??
    'Review current tunnel values, local ownership and native dependencies before preparing a change.'
  );
}
