export const treeParameterMessages: Record<string, string> = {
  STP_RSTP_MUTUALLY_EXCLUSIVE: 'STP and RSTP must not be enabled together.',
  SPANNING_TREE_INTEGER_REQUIRED:
    'Use a decimal whole number for this parameter.',
  SPANNING_TREE_PARAMETER_RANGE: 'This parameter is outside its native range.',
  STP_MAX_AGE_HELLO_RELATION:
    'STP max age must be at least twice hello time plus two seconds.',
  STP_FORWARD_DELAY_MAX_AGE_RELATION:
    'STP max age must not exceed twice forward delay minus two seconds.',
  RSTP_PRIORITY_MULTIPLE_4096:
    'RSTP priority must be a multiple of 4096. OVS rounds other values down.',
  RSTP_FORWARD_DELAY_MAX_AGE_RELATION:
    'RSTP max age must not exceed twice forward delay minus two seconds.',
  STP_HELLO_TIME_NATIVE_UNIT_CAVEAT:
    'Explicit STP hello time needs runtime verification. Tested OVS installs 1 second for configured values from 2 to 10 seconds.',
};

export function treeGateMessage(code: string): string {
  const messages: Record<string, string> = {
    SPANNING_TREE_NATIVE_WITNESS_REQUIRED:
      'Execution requires a fresh verified native runtime before dispatch and after installation.',
    SPANNING_TREE_EXECUTION_AUTHORITY_REQUIRED:
      'Execution requires separate Bridge authorization and a qualified native runtime source.',
    SPANNING_TREE_ORIGINAL_PARAMETERS_UNPROVEN:
      'The captured native parameters must be supported before a reversible change is available.',
    SPANNING_TREE_MULTICAST_COMBINATION_UNVERIFIED:
      'Spanning tree changes on a Bridge with multicast snooping need separate qualification.',
    SPANNING_TREE_ISOLATION_COMBINATION_UNVERIFIED:
      'Spanning tree changes with protected Ports need separate qualification.',
    RSTP_FORWARD_TIMER_ORDER_UNPROVEN:
      'These RSTP timers cannot be installed in the native setter order. Review a smaller change.',
    RSTP_ROLLBACK_TIMER_ORDER_UNPROVEN:
      'The original RSTP timers cannot be restored in one native pass. Review a smaller change.',
    RSTP_INITIAL_TIMER_ORDER_UNPROVEN:
      'This protocol change would need additional timer steps to install or restore the requested RSTP values.',
    SPANNING_TREE_EXECUTION_PENDING:
      'Draft review is available. Execution and safe timer recovery have not been verified, so Apply remains blocked.',
    SPANNING_TREE_BRIDGE_AUTHORITY_REQUIRED:
      'This Bridge requires independent authorization before a draft can be prepared.',
    SPANNING_TREE_CONFIGURATION_UNKNOWN:
      'Complete fresh native configuration is required before preparing a draft.',
    SPANNING_TREE_SCHEMA_UNSUPPORTED:
      'Draft preparation is unavailable for this native schema.',
    SPANNING_TREE_DEPENDENCY_CHANGED:
      'Connected objects or persistent configuration changed. Discard this intent and review the Bridge before preparing a new draft.',
    FIELD_CONFLICT:
      'Live configuration changed after this draft was captured. Discard this intent and review the current Bridge.',
    OBJECT_BINDING_CHANGED:
      'The captured Bridge identity is no longer current. Review the current Bridge and prepare a new draft.',
    GENERATION_RECONCILIATION_REQUIRED:
      'The OVS instance changed. Review current identities before preparing a new draft.',
    SCHEMA_CHANGED:
      'The native schema changed. Discard this intent and review the current Bridge.',
    CAPABILITY_DENIED:
      'Current permissions do not permit preparing this spanning tree draft.',
    AUTHORITY_POLICY_CHANGED:
      'Draft authorization changed. Discard this intent and review the current Bridge.',
  };
  return (
    treeParameterMessages[code] ??
    messages[code] ??
    'This draft needs a fresh review of its configuration, permissions and connected objects.'
  );
}
