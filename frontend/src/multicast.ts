export function multicastMessage(code: string | null | undefined): string {
  const messages: Record<string, string> = {
    MULTICAST_OBSERVATION_STALE:
      'This configuration observation is not current. Wait for a fresh native source before preparing a change.',
    CONFIGURATION_WITHHELD:
      'Your current permissions do not allow review of this configuration.',
    MULTICAST_GROUP_SOURCE_UNAVAILABLE:
      'A qualified native group observation source is not configured. Group membership is unavailable.',
    MULTICAST_GROUP_OBSERVATION_UNPROVEN:
      'The native group observation could not be verified. Group membership is unavailable.',
    MULTICAST_SNOOPING_DISABLED:
      'Snooping is disabled. The native group table is unavailable.',
    MULTICAST_ASSOCIATION_STALE:
      'The Bridge observation is stale. Current group membership is unavailable.',
    MULTICAST_ASSOCIATION_CHANGED:
      'The Bridge or connected Port association changed while reading. Current group membership is unavailable.',
    MULTICAST_PORT_ASSOCIATION_UNPROVEN:
      'A native group could not be matched to a current Port. Group membership is unavailable.',
    MULTICAST_NATIVE_FORWARDING_REVIEW_REQUIRED:
      'Snooping changes multicast forwarding on this Bridge. Review group listeners, router ports and management connectivity before applying.',
    SAFE_APPLY_REQUIRED:
      'This change requires Safe Apply, management reachability and explicit confirmation.',
    CAPABILITY_DENIED:
      'Your current permissions allow review but do not allow multicast changes.',
    MULTICAST_AUTHORITY_REQUIRED:
      'This Bridge needs independent local authorization before a multicast change is available.',
    MULTICAST_SCHEMA_UNSUPPORTED:
      'This OVS schema does not provide a supported snooping configuration.',
    MULTICAST_ADVANCED_POLICY_UNQUALIFIED:
      'Advanced multicast flooding or policy settings are present. This combination remains read-only and its native values are preserved.',
    MULTICAST_ORIGINAL_UNQUALIFIED:
      'The captured native settings are outside the qualified range. Review the original values before changing them.',
    MULTICAST_BRIDGE_COMBINATION_UNQUALIFIED:
      'Multicast changes with spanning tree have not been qualified.',
    MULTICAST_ISOLATION_COMBINATION_UNQUALIFIED:
      'Multicast changes with protected Ports have not been qualified.',
    MULTICAST_FOREIGN_DOMAIN_UNQUALIFIED:
      'Review the controller, Mirror and exporter dependencies. This combination has not been qualified.',
    MULTICAST_PORT_COMBINATION_UNQUALIFIED:
      'Multicast changes with Bond, LACP or QoS dependencies have not been qualified.',
    MULTICAST_DEPENDENCY_CHANGED:
      'Connected objects or configuration changed. Discard this intent and review the current Bridge.',
    FIELD_CONFLICT:
      'Live multicast configuration changed after capture. Discard this intent and review the current Bridge.',
    AUTHORITY_POLICY_CHANGED:
      'Multicast authorization changed. Review current permissions and local ownership before preparing a new change.',
    GENERATION_RECONCILIATION_REQUIRED:
      'The OVS instance changed. Review current identities before preparing a new change.',
  };
  return (
    messages[code ?? ''] ??
    'Review fresh Bridge and Port details, permissions and supported native dependencies before preparing a change.'
  );
}
