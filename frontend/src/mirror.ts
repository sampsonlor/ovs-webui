export const mirrorOperations = ['mirror.session.create', 'mirror.session.configure', 'mirror.session.delete'] as const;
export function isMirrorOperation(operation?: string) {
  return typeof operation === 'string' && (mirrorOperations as readonly string[]).includes(operation);
}
export function mirrorMessage(code?: string) {
  const messages: Record<string,string> = {
    CAPABILITY_DENIED:'Your current permissions allow review but do not allow Mirror changes.',
    MIRROR_NATIVE_IMAGE_UNKNOWN:'The complete native Mirror configuration could not be read.',
    MIRROR_CONFIGURATION_UNKNOWN:'The complete native Mirror configuration could not be read.',
    MIRROR_PORT_BINDING_UNPROVEN:'A selected or output Port no longer has a current native identity.',
    MIRROR_PARENT_UNPROVEN:'The current Bridge association for this Mirror session could not be established.',
    MIRROR_ADVANCED_FILTER_UNQUALIFIED:'This session uses an advanced native packet filter. Its configuration stays read-only.',
    MIRROR_SCHEMA_OR_GRAPH_UNSUPPORTED:'This native schema or connected Bridge configuration is outside the supported Mirror workflow.',
    MIRROR_AUTHORITY_OR_GRAPH_REQUIRED:'Current local ownership of the session, Bridge and affected Ports is required before preparing a change.',
    MIRROR_OBSERVATION_STALE:'Wait for a fresh native observation before preparing a Mirror change.',
    MIRROR_PORT_DETAIL_TRUNCATED:'The connected Port list is incomplete. Review the full native Bridge before preparing a change.',
    MIRROR_OUTPUT_SELECTION_CONFLICT:'A selected Port is reserved as another session’s output. Review the existing Mirror sessions.',
    MIRROR_PORT_ON_OTHER_BRIDGE:'Every selected and output Port must belong to this same Bridge.',
    MIRROR_LOCAL_INTERNAL_OUTPUT_PROTECTED:'The Bridge’s local internal Port cannot be reserved as a Mirror output.',
    MIRROR_DEPENDENCY_CHANGED:'Connected native configuration changed. Discard this draft and review the current Bridge and Mirror sessions.',
    MIRROR_AUTHORITY_REVISION_CHANGED:'Local Mirror ownership changed. Discard this draft and review current permissions.',
    TOPOLOGY_AUTHORITY_REQUIRED:'One of the affected objects has no current local management ownership.',
    TOPOLOGY_CREATION_GRANT_REQUIRED:'This session name is not available for local management on the selected Bridge.',
    HIGH_RISK_TOPOLOGY_SAFE_APPLY_REQUIRED:'This change requires Safe Apply, management reachability and explicit confirmation.',
    MANAGEMENT_PATH_AND_SOLE_UPLINK_REVIEW_REQUIRED:'Review the management path, output Port and sole uplink before applying.',
  };
  return messages[code ?? ''] ?? 'Review the native selectors, reserved output, local ownership and connected configuration before preparing a Mirror change.';
}
