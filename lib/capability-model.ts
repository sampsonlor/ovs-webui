import type { ControlState } from './change-control';
import type { View } from './ovs-model';
import {
  accelerationStates,
  assessAcceleration,
  projectAccelerationRecord,
  observationFreshness,
  observationText,
  type AccelerationState,
  type AccelerationSnapshot,
} from './acceleration-model.ts';
import { flowFreshness, type FlowSnapshot } from './openflow-model.ts';
import {
  capabilityTtlMs,
  nativeFreshness,
  nativeTarget,
  type NativeCapabilityProof,
} from './native-capability.ts';

export const capabilityStates = accelerationStates;
export type CapabilityState = AccelerationState;
export type CapabilityId =
  | 'bridge'
  | 'vlan'
  | 'bond'
  | 'protected'
  | 'openflow'
  | 'dpdk'
  | 'offload'
  | 'diagnostics'
  | 'lifecycle';
export type CapabilityLevel =
  | 'Observe'
  | 'Basic Manage'
  | 'Manage'
  | 'Manage if native';
export const capabilityReviewLabels = {
  normal: 'Normal · native action eligible',
  enabled: 'Native feature already enabled',
  missing: 'Native prerequisite missing',
  unsupported: 'Native primitive unsupported',
  unknown: 'Native support unknown',
  external: 'External controller authority',
  unauthorized: 'Native permission withdrawn',
  unsafe: 'Checkpoint / rollback unavailable',
  'management-path': 'Management path affected',
  'reauth-required': 'High risk · step-up missing',
  degraded: 'Native provider degraded',
  unavailable: 'Native provider unavailable',
  stale: 'Registry observations stale',
  'generation-mismatch': 'Registry generation changed',
  empty: 'Registry returned no observations',
  failed: 'Registry read failed',
} as const;
export type CapabilityReviewCase = keyof typeof capabilityReviewLabels;
type Definition = {
  id: CapabilityId;
  code: string;
  title: string;
  domain: string;
  level: CapabilityLevel;
  permission: string;
  view: View;
  boundary: string;
};
export const capabilityCatalog: Definition[] = [
  {
    id: 'bridge',
    code: 'ovs.bridge.manage',
    title: 'Bridge configuration',
    domain: 'Switching',
    level: 'Manage',
    permission: 'bridge.manage',
    view: 'bridges',
    boundary:
      'Native Bridge fields enter the shared Candidate. Unknown fields remain preserved.',
  },
  {
    id: 'vlan',
    code: 'ovs.port.vlan',
    title: 'Port VLAN configuration',
    domain: 'Switching',
    level: 'Basic Manage',
    permission: 'port.vlan.manage',
    view: 'ports',
    boundary:
      'The bounded Port VLAN editor stages intent. Advanced native settings remain explicit.',
  },
  {
    id: 'bond',
    code: 'ovs.port.bond',
    title: 'Bond / LACP',
    domain: 'Switching',
    level: 'Manage',
    permission: 'bond.manage',
    view: 'bonds',
    boundary:
      'A Bond is a Port with member Interfaces. Configuration enters the shared Candidate.',
  },
  {
    id: 'protected',
    code: nativeTarget.code,
    title: 'Native protected Port',
    domain: 'Switching',
    level: 'Manage if native',
    permission: 'port.isolation.manage',
    view: 'capabilities',
    boundary:
      'Protected peers cannot exchange traffic through the declared local NORMAL path. Unprotected peers remain reachable. This is not a general Port Security policy.',
  },
  {
    id: 'openflow',
    code: 'ovs.openflow.observe',
    title: 'OpenFlow Viewer',
    domain: 'Switching',
    level: 'Observe',
    permission: 'openflow.read',
    view: 'openflow-viewer',
    boundary:
      'This reviewed slice collects bounded flow snapshots. External-controller and unknown domains stay read-only.',
  },
  {
    id: 'dpdk',
    code: 'acceleration.dpdk.observe',
    title: 'DPDK runtime',
    domain: 'Visibility',
    level: 'Observe',
    permission: 'acceleration.observe',
    view: 'acceleration-overview',
    boundary:
      'Phase 1 observes DPDK. Enablement, memory provisioning and tuning are performed externally.',
  },
  {
    id: 'offload',
    code: 'acceleration.offload.observe',
    title: 'Hardware offload',
    domain: 'Visibility',
    level: 'Observe',
    permission: 'acceleration.observe',
    view: 'acceleration-overview',
    boundary:
      'Phase 1 observes offload. Driver, firmware and switchdev changes are performed externally.',
  },
  {
    id: 'diagnostics',
    code: 'diagnostics.bounded',
    title: 'Bounded diagnostics',
    domain: 'Operations',
    level: 'Observe',
    permission: 'diagnostics.run',
    view: 'diagnostics-hub',
    boundary:
      'Declared templates use fixed resource budgets and durable Job semantics; no arbitrary shell.',
  },
  {
    id: 'lifecycle',
    code: 'ovs.lifecycle.manage',
    title: 'OVS lifecycle',
    domain: 'Administration',
    level: 'Manage if native',
    permission: 'ovs.lifecycle.manage',
    view: 'system-health',
    boundary:
      'No lifecycle execution provider is declared in this sample. Inspect health or use external administration.',
  },
];
export type CapabilityFact = {
  label: string;
  value: string;
  reason: string;
  owner: string;
};
export type CapabilityRow = Definition & {
  state: CapabilityState;
  reason: string;
  provider: string;
  providerState: 'Available' | 'Degraded' | 'Unavailable';
  authority: string;
  source: string;
  version: string;
  observedAt: number;
  generation: number | null;
  freshness: 'Fresh' | 'Stale' | 'Generation mismatch' | 'Unavailable';
  scope: string;
  canEnable: boolean;
  prerequisites: CapabilityFact[];
};
export type CapabilitySnapshot = {
  id: string;
  observedAt: number;
  generation: number;
  empty: boolean;
  proof: NativeCapabilityProof | null;
};
export type CapabilitySignals = {
  acceleration: {
    snapshot: AccelerationSnapshot | null;
    failure: string | null;
  };
  openFlow: { snapshot: FlowSnapshot | null; failure: string | null };
};
export function capabilityReadBlock(scenario: string) {
  if (scenario === 'permission-denied')
    return 'Capability access denied · capabilities.read is required.';
  if (
    ['network-loss', 'provider-unavailable', 'error', 'loading'].includes(
      scenario,
    )
  )
    return 'The capability registry cannot supply current observations.';
  return null;
}
export function captureCapabilities(
  reviewCase: CapabilityReviewCase,
  now: number,
  generation: number,
  enabled = false,
): CapabilitySnapshot {
  const observedAt = reviewCase === 'stale' ? now - capabilityTtlMs - 1 : now;
  const epoch =
    reviewCase === 'generation-mismatch' ? generation - 1 : generation;
  const proof: NativeCapabilityProof = {
    code: nativeTarget.code,
    target: nativeTarget.target,
    bridge: nativeTarget.bridge,
    instance: nativeTarget.instance,
    observedAt,
    source: `cap-${now}/native`,
    generation: epoch,
    normalSwitching: true,
    supported: true,
    enabled,
    canEnable: true,
    authority: 'Local OVS',
    authorized: true,
    provider: 'Available',
    prerequisites: true,
    validation: true,
    checkpoint: true,
    rollback: true,
    impactReviewed: true,
    managementPath: 'Unaffected',
    highRisk: false,
    reauthenticated: false,
  };
  if (reviewCase === 'enabled') proof.enabled = true;
  if (reviewCase === 'missing') proof.prerequisites = false;
  if (reviewCase === 'unsupported') proof.supported = false;
  if (reviewCase === 'unknown') proof.supported = null;
  if (reviewCase === 'external') proof.authority = 'External controller';
  if (reviewCase === 'unauthorized') proof.authorized = false;
  if (reviewCase === 'unsafe') {
    proof.rollback = false;
    proof.checkpoint = false;
  }
  if (reviewCase === 'management-path') proof.managementPath = 'Affected';
  if (reviewCase === 'reauth-required') proof.highRisk = true;
  if (reviewCase === 'degraded') proof.provider = 'Degraded';
  if (reviewCase === 'unavailable') proof.provider = 'Unavailable';
  return {
    id: `cap-${now}`,
    observedAt,
    generation: epoch,
    empty: reviewCase === 'empty',
    proof: reviewCase === 'empty' ? null : proof,
  };
}
function assessNative(
  proof: NativeCapabilityProof | null | undefined,
  scenario: string,
  generation: number,
  now: number,
): { state: CapabilityState; reason: string } {
  if (
    nativeFreshness(proof, generation, now) !== 'Fresh' ||
    capabilityReadBlock(scenario) ||
    scenario === 'stale' ||
    !proof ||
    proof.provider === 'Unavailable'
  )
    return {
      state: 'Unknown',
      reason:
        'Fresh native capability evidence is unavailable. Review source and generation before preparing a change.',
    };
  if (proof.supported === false)
    return {
      state: 'Unsupported',
      reason:
        'The provider explicitly reports that the native primitive is unsupported for this target.',
    };
  if (proof.supported !== true)
    return {
      state: 'Unknown',
      reason: 'The provider has not established native support.',
    };
  if (proof.enabled === true && proof.prerequisites === true)
    return {
      state: 'Enabled',
      reason:
        'The native provider reports protected=true. Authority and safety still govern management actions.',
    };
  if (proof.enabled === false && proof.prerequisites === false)
    return {
      state: 'Missing prerequisites',
      reason:
        'The required local NORMAL switching profile is not ready. Inspect the prerequisite owner.',
    };
  if (proof.enabled === false && proof.prerequisites === true)
    return {
      state: 'Available',
      reason:
        'The native primitive is supported and not enabled. A configuration action additionally requires all four gates.',
    };
  return {
    state: 'Unknown',
    reason:
      'Configuration and prerequisite evidence are incomplete or inconsistent.',
  };
}

// Every surface consumes this projection. Acceleration assessments are delegated
// to the accepted model; a registry refresh cannot relabel an acceleration sample.
export function deriveCapabilities(
  snapshot: CapabilitySnapshot | null,
  control: ControlState,
  signals: CapabilitySignals,
  now: number,
  failed = false,
): CapabilityRow[] {
  if (control.scenario === 'permission-denied') return [];
  const unavailable = failed || !!capabilityReadBlock(control.scenario);
  const empty = !snapshot || snapshot.empty || control.scenario === 'empty';
  const registryFreshness =
    empty || unavailable
      ? 'Unavailable'
      : snapshot.generation !== control.generation
        ? 'Generation mismatch'
        : control.scenario === 'stale' ||
            !Number.isFinite(now) ||
            !Number.isFinite(snapshot.observedAt) ||
            now < snapshot.observedAt ||
            now - snapshot.observedAt >= capabilityTtlMs
          ? 'Stale'
          : 'Fresh';
  return capabilityCatalog.map((definition): CapabilityRow => {
    const row: CapabilityRow = {
      ...definition,
      state: 'Unknown',
      reason: 'No current provider observation is available.',
      provider: 'Capability registry',
      providerState: unavailable
        ? 'Unavailable'
        : ['degraded', 'provider-degraded'].includes(control.scenario)
          ? 'Degraded'
          : 'Available',
      authority: 'Local OVS',
      source: snapshot?.id ?? 'Not observed',
      version: 'Synthetic contract v0.1',
      observedAt: empty ? 0 : snapshot!.observedAt,
      generation: empty ? null : snapshot!.generation,
      freshness: registryFreshness,
      scope: 'Bounded prototype inventory',
      canEnable: false,
      prerequisites: [],
    };
    if (['bridge', 'vlan', 'bond', 'diagnostics'].includes(row.id)) {
      row.provider =
        row.id === 'diagnostics'
          ? 'Diagnostic template registry'
          : 'OVSDB schema / inventory provider';
      if (registryFreshness === 'Fresh') {
        row.state = 'Enabled';
        row.reason =
          'The declared synthetic provider exposes this reviewed operation. Per-object authority and validation still apply.';
      }
      row.prerequisites = [
        {
          label: 'Supported provider contract',
          value: registryFreshness === 'Fresh' ? 'Present' : 'Unknown',
          owner: 'Provider',
          reason: 'Only the named reviewed slice is covered.',
        },
      ];
    } else if (row.id === 'lifecycle') {
      row.authority = 'Unknown';
      row.provider = 'Lifecycle provider';
      if (registryFreshness === 'Fresh') {
        row.state = 'Unsupported';
        row.reason =
          'This synthetic registry explicitly has no lifecycle executor. No install, restart or upgrade action is offered.';
      }
      row.prerequisites = [
        {
          label: 'Lifecycle execution provider',
          value: 'Not declared',
          owner: 'External administrator',
          reason: 'Provisioning is outside this prototype.',
        },
      ];
    } else if (row.id === 'protected') {
      const proof = empty ? null : control.nativeCapability;
      Object.assign(
        row,
        assessNative(
          proof,
          unavailable ? 'provider-unavailable' : control.scenario,
          control.generation,
          now,
        ),
        {
          provider: 'Native OVS protected-Port provider',
          providerState: unavailable
            ? 'Unavailable'
            : ['degraded', 'provider-degraded'].includes(control.scenario) &&
                proof?.provider === 'Available'
              ? 'Degraded'
              : (proof?.provider ?? 'Unavailable'),
          authority: proof?.authority ?? 'Unknown',
          scope: `Bridge/${nativeTarget.bridge} → ${nativeTarget.target} → Interface/${nativeTarget.interface}`,
          observedAt: proof?.observedAt ?? 0,
          source: proof?.source ?? 'Not observed',
          generation: proof?.generation ?? null,
          freshness: unavailable
            ? 'Unavailable'
            : control.scenario === 'stale'
              ? 'Stale'
              : nativeFreshness(proof, control.generation, now),
          canEnable: !unavailable && proof?.canEnable === true,
        },
      );
      row.prerequisites = [
        {
          label: 'Local NORMAL switching profile',
          value: observationText(proof?.prerequisites ?? null),
          owner: 'Local host provider',
          reason:
            'External controller policy is excluded; resolve host prerequisites externally.',
        },
        {
          label: 'Management-path assessment',
          value: proof?.managementPath ?? 'Unknown',
          owner: 'Change safety provider',
          reason: 'This bounded action requires an unaffected management path.',
        },
        {
          label: 'Validation / checkpoint / rollback',
          value: proof
            ? `${observationText(proof.validation)} / ${observationText(proof.checkpoint)} / ${observationText(proof.rollback)}`
            : 'Unknown',
          owner: 'Shared change control',
          reason: 'Checkpoint and compare-before-rollback must be available.',
        },
      ];
    } else if (row.id === 'dpdk' || row.id === 'offload') {
      const sample = signals.acceleration.snapshot;
      const original = sample?.records.find((record) => record.id === row.id);
      row.authority = 'External platform';
      row.provider =
        row.id === 'dpdk'
          ? 'DPDK capability / runtime providers'
          : 'NIC offload provider';
      row.source = sample?.id ?? 'Not observed';
      row.observedAt = sample?.capturedAt ?? 0;
      row.generation = sample?.generation ?? null;
      row.freshness = 'Unavailable';
      row.providerState = 'Unavailable';
      if (original) {
        const scenario =
          signals.acceleration.failure || capabilityReadBlock(control.scenario)
            ? 'provider-unavailable'
            : control.scenario;
        const record = projectAccelerationRecord(original, scenario);
        Object.assign(
          row,
          assessAcceleration(record, now, control.generation, scenario),
          {
            scope: record.scope,
            providerState: record.provider,
            freshness:
              record.provider === 'Unavailable' || !record.facts[0]
                ? 'Unavailable'
                : scenario === 'stale'
                  ? 'Stale'
                  : observationFreshness(
                      record.facts[0],
                      now,
                      control.generation,
                    ),
          },
        );
        row.prerequisites = record.facts
          .filter((fact) => record.prerequisites.includes(fact.key))
          .map((fact) => ({
            label: fact.label,
            value: observationText(fact.value),
            owner: fact.source,
            reason: fact.note,
          }));
      }
    } else if (row.id === 'openflow') {
      const sample = signals.openFlow.snapshot;
      row.provider = 'OpenFlow collection provider';
      row.authority = sample?.source.authority ?? 'Unknown';
      row.source = sample?.id ?? 'Not observed';
      row.observedAt = sample?.observedAt ?? 0;
      row.generation = null;
      row.scope = sample
        ? `Bridge/${sample.query.bridge}`
        : 'Bounded flow collection';
      row.providerState =
        !sample ||
        signals.openFlow.failure ||
        capabilityReadBlock(control.scenario)
          ? 'Unavailable'
          : sample.providerDegraded ||
              ['degraded', 'provider-degraded'].includes(control.scenario)
            ? 'Degraded'
            : 'Available';
      row.freshness =
        !sample ||
        signals.openFlow.failure ||
        capabilityReadBlock(control.scenario)
          ? 'Unavailable'
          : control.scenario === 'stale'
            ? 'Stale'
            : flowFreshness(sample, now);
      if (sample && row.freshness === 'Fresh') {
        row.state = 'Enabled';
        row.reason =
          'The shared bounded collector returned a snapshot. This does not grant flow modification authority.';
      }
    }
    return row;
  });
}

export function capabilityCounts(rows: CapabilityRow[]) {
  return Object.fromEntries(
    capabilityStates.map((state) => [
      state,
      rows.filter((row) => row.state === state).length,
    ]),
  ) as Record<CapabilityState, number>;
}
export function filterCapabilities(
  rows: CapabilityRow[],
  search: string,
  state: string,
  domain: string,
) {
  const query = search.trim().toLowerCase();
  return rows.filter(
    (row) =>
      (state === 'all' || row.state === state) &&
      (domain === 'all' || row.domain === domain) &&
      [row.title, row.code, row.domain, row.provider, row.scope]
        .join(' ')
        .toLowerCase()
        .includes(query),
  );
}
