export function isolationMessage(code: string | null | undefined): string {
  const messages: Record<string, string> = {
    PROTECTED_PORT_NATIVE_SEMANTICS_REVIEW_REQUIRED:
      'Protected ports cannot communicate with other protected ports on this Bridge. Review management connectivity before applying.',
    SAFE_APPLY_REQUIRED:
      'This change requires Safe Apply, management reachability and explicit confirmation.',
    CAPABILITY_DENIED:
      'Your current permissions allow review but do not allow isolation changes.',
    ISOLATION_AUTHORITY_REQUIRED:
      'This Port needs independent local authorization before an isolation change is available.',
    ISOLATION_SCHEMA_UNSUPPORTED:
      'This native OVS schema does not provide a supported protected Port field.',
    ISOLATION_OBSERVATION_STALE:
      'The native observation is stale. Wait for fresh data before preparing a change.',
    ISOLATION_SYSTEM_BRIDGE_REQUIRED:
      'Isolation changes require a supported system Bridge.',
    ISOLATION_LOCAL_PORT_UNSUPPORTED:
      'Changing isolation on the Bridge local Port is not supported.',
    ISOLATION_BRIDGE_COMBINATION_UNQUALIFIED:
      'Isolation with spanning tree or multicast snooping has not been qualified on this Bridge.',
    ISOLATION_FOREIGN_DOMAIN_UNQUALIFIED:
      'Review the Bridge controller, Mirror, QoS and exporter dependencies. This combination has not been qualified.',
    ISOLATION_MIRROR_RELATION_UNQUALIFIED:
      'An existing Mirror relationship prevents a qualified isolation change.',
    ISOLATION_OFFLOAD_UNQUALIFIED:
      'Isolation changes with hardware offload have not been qualified.',
    ISOLATION_PORT_COMBINATION_UNQUALIFIED:
      'Isolation changes with Bond, LACP or QoS dependencies have not been qualified.',
    ISOLATION_VLAN_COMBINATION_UNQUALIFIED:
      'This VLAN configuration is outside the qualified isolation scope.',
    ISOLATION_INTERFACE_COMBINATION_UNQUALIFIED:
      'This Interface type or association is outside the qualified isolation scope.',
    FIELD_CONFLICT:
      'Live isolation changed after this draft was captured. Discard this intent and review the current Port.',
    ISOLATION_DEPENDENCY_CHANGED:
      'Connected objects or configuration changed. Discard this intent and review the current Port and Bridge.',
    AUTHORITY_POLICY_CHANGED:
      'Isolation authorization changed. Discard this intent and review current permissions and local ownership.',
    OBJECT_BINDING_CHANGED:
      'The captured Port identity is no longer current. Review the current Port before preparing a new change.',
    GENERATION_RECONCILIATION_REQUIRED:
      'The OVS instance changed. Review current identities before preparing a new change.',
    NATIVE_CONFIGURATION_UNKNOWN:
      'Fresh, complete native configuration is required before preparing a reversible change.',
  };
  return messages[code ?? ''] ?? 'Review fresh Port and Bridge details, current permissions and supported dependencies before preparing a change.';
}
