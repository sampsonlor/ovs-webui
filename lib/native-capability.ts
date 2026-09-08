// Dedicated synthetic NORMAL-switching target. Existing OpenFlow samples are
// externally controlled and must never be used to demonstrate local enablement.
export const nativeTarget = {
  code: 'ovs.port.protected',
  port: 'isolation-demo',
  target: 'Port/isolation-demo',
  bridge: 'br-native-demo',
  interface: 'isolation-demo0',
  instance: 'ovs-synthetic-01',
} as const;

export type NativeCapabilityProof = {
  code: 'ovs.port.protected';
  target: 'Port/isolation-demo';
  bridge: 'br-native-demo';
  instance: 'ovs-synthetic-01';
  normalSwitching: boolean;
  observedAt: number;
  source: string;
  generation: number;
  supported: boolean | null;
  enabled: boolean | null;
  canEnable: boolean;
  authority: 'Local OVS' | 'External controller' | 'Unknown';
  authorized: boolean;
  provider: 'Available' | 'Degraded' | 'Unavailable';
  prerequisites: boolean | null;
  validation: boolean;
  checkpoint: boolean;
  rollback: boolean;
  impactReviewed: boolean;
  managementPath: 'Unaffected' | 'Affected' | 'Unknown';
  highRisk: boolean;
  reauthenticated: boolean;
};
export const capabilityTtlMs = 60_000;

export function nativeFreshness(
  proof: NativeCapabilityProof | null | undefined,
  generation: number,
  now: number,
) {
  if (!proof) return 'Unavailable';
  if (
    proof.code !== nativeTarget.code ||
    proof.target !== nativeTarget.target ||
    proof.bridge !== nativeTarget.bridge ||
    proof.instance !== nativeTarget.instance ||
    proof.generation !== generation
  )
    return 'Generation mismatch';
  if (
    !Number.isFinite(now) ||
    !Number.isFinite(proof.observedAt) ||
    now < proof.observedAt ||
    now - proof.observedAt >= capabilityTtlMs
  )
    return 'Stale';
  return 'Fresh';
}

export function nativePolicyGates(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
) {
  const fresh = nativeFreshness(proof, generation, now) === 'Fresh';
  const service = ![
    'loading',
    'error',
    'network-loss',
    'provider-unavailable',
    'provider-degraded',
    'degraded',
    'stale',
    'empty',
  ].includes(scenario);
  return [
    {
      id: 'capability',
      label: 'Provider capability',
      passed:
        !!proof &&
        fresh &&
        service &&
        proof.code === nativeTarget.code &&
        proof.target === nativeTarget.target &&
        proof.bridge === nativeTarget.bridge &&
        proof.supported === true &&
        proof.canEnable === true &&
        proof.provider === 'Available' &&
        proof.prerequisites === true,
      reason:
        'A current provider must explicitly support this target, prerequisites and a safe native operation.',
    },
    {
      id: 'authority',
      label: 'Local authority',
      passed:
        proof?.authority === 'Local OVS' && proof.normalSwitching === true,
      reason:
        'The target field and NORMAL switching path must be locally controlled. External or unknown ownership stays read-only.',
    },
    {
      id: 'authorization',
      label: 'Required permission',
      passed: scenario !== 'permission-denied' && proof?.authorized === true,
      reason:
        'port.isolation.manage is required. Information depth does not grant permission.',
    },
    {
      id: 'safety',
      label: 'Validation and recovery',
      passed:
        !!proof &&
        proof.validation === true &&
        proof.checkpoint === true &&
        proof.rollback === true &&
        proof.impactReviewed === true &&
        proof.managementPath === 'Unaffected' &&
        (!proof.highRisk || proof.reauthenticated === true),
      reason:
        'This bounded operation requires validation, reviewed impact, a checkpoint and protected rollback. Affected or unknown management paths and missing step-up authorization are blocked.',
    },
  ];
}

export function nativeRollbackBlock(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
) {
  if (scenario === 'permission-denied' || proof?.authorized !== true)
    return 'Current native rollback authorization is required. Deadline recovery remains manager-owned.';
  if (
    nativeFreshness(proof, generation, now) !== 'Fresh' ||
    proof?.provider !== 'Available' ||
    proof.authority !== 'Local OVS' ||
    !proof.normalSwitching ||
    !proof.rollback ||
    !proof.checkpoint ||
    proof.enabled !== true
  )
    return 'Current local authority, checkpoint and compare-before-rollback evidence are required.';
  if (
    [
      'network-loss',
      'provider-unavailable',
      'provider-degraded',
      'degraded',
      'error',
      'loading',
      'empty',
      'stale',
      'drift',
      'conflict',
      'rollback-conflict',
    ].includes(scenario)
  )
    return 'The current observation cannot authorize manual rollback. Inspect the original transaction evidence.';
  return null;
}

export function nativeConfirmBlock(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
) {
  return (
    nativePolicyBlock(proof, scenario, generation, now) ??
    (proof?.enabled !== true
      ? 'Current native Applied evidence must match protected=true before confirmation.'
      : null)
  );
}

export function nativePolicyBlock(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
): string | null {
  const failed = nativePolicyGates(proof, scenario, generation, now).find(
    (gate) => !gate.passed,
  );
  return failed ? `${failed.label}: ${failed.reason}` : null;
}

export function nativeStageBlock(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
  desktop: boolean,
  transactionStatus: string,
): string | null {
  if (!desktop) return 'Prepare native configuration on desktop.';
  if (
    [
      'countdown',
      'outcome-unknown',
      'rollback-conflict',
      'degraded',
      'needs-attention',
    ].includes(transactionStatus)
  )
    return 'The existing transaction must be resolved first.';
  const policy = nativePolicyBlock(proof, scenario, generation, now);
  if (policy) return policy;
  if (scenario !== 'normal')
    return 'Resolve the shared review or drift state before preparing a native change.';
  if (proof?.enabled !== false)
    return 'The provider must explicitly confirm that this native feature is not enabled.';
  return null;
}
