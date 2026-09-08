// A bounded synthetic native-action policy, separate from presentation mode.
export type NativeCapabilityProof = {
  code: 'ovs.port.protected';
  target: 'Port/server-07';
  bridge: 'br-fabric';
  observedAt: number;
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

export function nativePolicyGates(proof: NativeCapabilityProof | null | undefined, scenario: string, generation: number, now: number) {
  const fresh = !!proof && Number.isFinite(proof.observedAt) && now >= proof.observedAt && now - proof.observedAt < capabilityTtlMs && proof.generation === generation;
  const service = !['loading', 'error', 'network-loss', 'provider-unavailable', 'provider-degraded', 'degraded', 'stale', 'empty'].includes(scenario);
  return [
    { id: 'capability', label: 'Provider capability', passed: !!proof && fresh && service && proof.code === 'ovs.port.protected' && proof.target === 'Port/server-07' && proof.bridge === 'br-fabric' && proof.supported === true && proof.canEnable === true && proof.provider === 'Available' && proof.prerequisites === true, reason: 'A current provider must explicitly support this target, prerequisites and a safe native operation.' },
    { id: 'authority', label: 'Local authority', passed: proof?.authority === 'Local OVS', reason: 'The target field and NORMAL switching path must be locally controlled. External or unknown ownership stays read-only.' },
    { id: 'authorization', label: 'Required permission', passed: scenario !== 'permission-denied' && proof?.authorized === true, reason: 'port.isolation.manage is required. Information depth does not grant permission.' },
    { id: 'safety', label: 'Validation and recovery', passed: !!proof && proof.validation === true && proof.checkpoint === true && proof.rollback === true && proof.impactReviewed === true && proof.managementPath === 'Unaffected' && (!proof.highRisk || proof.reauthenticated === true), reason: 'This bounded operation requires validation, reviewed impact, a checkpoint and protected rollback. Affected or unknown management paths and missing step-up authorization are blocked.' },
  ];
}

export function nativePolicyBlock(proof: NativeCapabilityProof | null | undefined, scenario: string, generation: number, now: number): string | null {
  const failed = nativePolicyGates(proof, scenario, generation, now).find((gate) => !gate.passed);
  return failed ? `${failed.label}: ${failed.reason}` : null;
}

export function nativeStageBlock(proof: NativeCapabilityProof | null | undefined, scenario: string, generation: number, now: number, desktop: boolean, transactionStatus: string): string | null {
  if (!desktop) return 'Prepare native configuration on desktop.';
  if (['countdown', 'outcome-unknown', 'rollback-conflict', 'degraded', 'needs-attention'].includes(transactionStatus)) return 'The existing transaction must be resolved first.';
  const policy = nativePolicyBlock(proof, scenario, generation, now);
  if (policy) return policy;
  if (scenario !== 'normal') return 'Resolve the shared review or drift state before preparing a native change.';
  if (proof?.enabled !== false) return 'The provider must explicitly confirm that this native feature is not enabled.';
  return null;
}
