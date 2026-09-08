import type { ControlState, EvidenceEntry } from './change-control';
import type { View } from './ovs-model';
import type { DiagnosticJobState } from '../app/prototype-model';
import {
  assessAcceleration,
  projectAccelerationRecord,
  type AccelerationSnapshot,
} from './acceleration-model.ts';
import { flowFreshness, type FlowSnapshot } from './openflow-model.ts';
import type { CapabilityRow } from './capability-model';

export const healthStates = [
  'Healthy',
  'Recovering',
  'Unknown',
  'Degraded',
  'Critical',
  'Recovery Required',
] as const;
export type HealthStatus = (typeof healthStates)[number];
export const healthDomains = [
  'OVS',
  'Datapath',
  'Providers',
  'Management plane',
  'Change safety',
  'Jobs',
] as const;
export type HealthDomain = (typeof healthDomains)[number];
export const healthReviewLabels = {
  normal: 'Normal · representative inventory',
  healthy: 'Healthy observation sample',
  degraded: 'Provider degraded',
  critical: 'OVS process stopped',
  'management-critical': 'API failed · independent datapath',
  unknown: 'Missing provider evidence',
  stale: 'Stale health observations',
  'generation-mismatch': 'Instance generation changed',
  recovering: 'Provider recovering',
  'recovery-required': 'Manager database recovery required',
  empty: 'No health observations',
  failed: 'Health read failed',
} as const;
export type HealthReviewCase = keyof typeof healthReviewLabels;
export type HealthAction =
  | { label: string; view: View }
  | { label: string; object: string }
  | { label: string; diagnostic: string; scope: string };
export type HealthComponent = {
  id: string;
  domain: HealthDomain;
  label: string;
  status: HealthStatus;
  reason: string;
  impact: string;
  value: string;
  code: string;
  source: string;
  observedAt: number;
  since: number;
  generation: number | null;
  freshness: 'Fresh' | 'Stale' | 'Unavailable';
  related: string;
  action: HealthAction;
  historicalStatus?: HealthStatus;
};
export type HealthSnapshot = {
  id: string;
  capturedAt: number;
  generation: number;
  reviewCase: HealthReviewCase;
  components: HealthComponent[];
};
export type HealthSignals = {
  capability?: { rows: CapabilityRow[]; failure: string | null };
  openFlow: { snapshot: FlowSnapshot | null; failure: string | null };
  acceleration: {
    snapshot: AccelerationSnapshot | null;
    failure: string | null;
  };
  diagnostic: { state: DiagnosticJobState; scope: string; reviewOnly: boolean };
};
export const healthTtlMs = 60_000;
export function healthReadBlock(scenario: string) {
  if (scenario === 'permission-denied')
    return 'Permission denied · health.read is required.';
  if (
    ['network-loss', 'provider-unavailable', 'error', 'loading'].includes(
      scenario,
    )
  )
    return 'The health observation service is unavailable. Retained observations are historical.';
  return null;
}
export function rollupHealth(
  components: Pick<HealthComponent, 'status'>[],
): HealthStatus {
  return components.length
    ? healthStates[
        Math.max(...components.map((item) => healthStates.indexOf(item.status)))
      ]
    : 'Unknown';
}

export function captureHealth(
  reviewCase: HealthReviewCase,
  now: number,
  generation: number,
): HealthSnapshot {
  const stamp = reviewCase === 'stale' ? now - healthTtlMs - 1 : now;
  const epoch =
    reviewCase === 'generation-mismatch' ? generation - 1 : generation;
  const rows: HealthComponent[] = [];
  const add = (
    id: string,
    domain: HealthDomain,
    label: string,
    source: string,
    value: string,
    related: string,
    action: HealthAction,
  ) => {
    rows.push({
      id,
      domain,
      label,
      source,
      value,
      related,
      action,
      code: `${id}.observed`,
      status: 'Healthy',
      reason: 'The declared synthetic check returned current evidence.',
      impact: 'This check covers its named component only.',
      observedAt: stamp,
      since: stamp,
      generation: epoch,
      freshness: 'Fresh',
    });
  };
  const evidence: HealthAction = {
    label: 'Open shared evidence',
    view: 'evidence',
  };
  const acceleration: HealthAction = {
    label: 'Inspect acceleration evidence',
    view: 'acceleration-overview',
  };
  add(
    'ovsdb',
    'OVS',
    'ovsdb-server',
    'OVSDB connection provider',
    'Connected · database identified',
    'OVS instance / ovs-synthetic-01',
    evidence,
  );
  add(
    'vswitchd',
    'OVS',
    'ovs-vswitchd',
    'OVS process / applied-state provider',
    'Running · application acknowledgement observed',
    'OVS instance / ovs-synthetic-01',
    evidence,
  );
  add(
    'schema',
    'OVS',
    'Schema / instance identity',
    'OVSDB schema and manager identity provider',
    'Schema recognized · generation matched',
    'OVS instance / ovs-synthetic-01',
    evidence,
  );
  add(
    'applied-state',
    'OVS',
    'Configured / applied state',
    'OVSDB and applied-state acknowledgement provider',
    'Configured and applied sample agree · health remains an independent observation',
    'OVS instance / ovs-synthetic-01',
    { label: 'Inspect configuration evidence', view: 'evidence' },
  );
  add(
    'restart',
    'OVS',
    'Process restart evidence',
    'Process lifecycle provider',
    'No restart transition in this bounded sample',
    'OVS instance / ovs-synthetic-01',
    evidence,
  );
  add(
    'packet-path',
    'Datapath',
    'Bridge / packet path',
    'Bounded datapath observation provider',
    'br-fabric · system datapath sample',
    'Bridge/br-fabric',
    {
      label: 'Prepare packet-path diagnostic',
      diagnostic: 'diag.ovs.datapath-trace',
      scope: 'Bridge/br-fabric',
    },
  );
  add(
    'link',
    'Datapath',
    'Port / Interface links',
    'Linux link provider',
    'Declared link observation sample',
    'Port/server-08',
    { label: 'Open related Port', object: 'Port/server-08' },
  );
  add(
    'linux',
    'Providers',
    'Linux link provider',
    'Provider availability registry',
    'Available · link source responding',
    'Port/bond-storage',
    {
      label: 'Prepare link diagnostic',
      diagnostic: 'diag.net.link-lacp',
      scope: 'Port/bond-storage',
    },
  );
  add(
    'dpdk-provider',
    'Providers',
    'DPDK provider',
    'DPDK health provider',
    'Initialization evidence available',
    'Host / OVS instance',
    acceleration,
  );
  add(
    'offload-provider',
    'Providers',
    'Offload provider',
    'NIC health provider',
    'Provider responding in this observation sample',
    'Bridge/br-offload',
    acceleration,
  );
  add(
    'telemetry',
    'Providers',
    'Telemetry provider',
    'Structured telemetry provider',
    'Observation channel responding',
    'OVS instance / ovs-synthetic-01',
    evidence,
  );
  for (const [id, label, value] of [
    ['api', 'webd / API', 'API probe accepted'],
    ['mgrd', 'mgrd', 'IPC handshake accepted'],
    ['web-db', 'web.db', 'Read probe completed'],
    ['manager-db', 'manager.db', 'Journal read probe completed'],
    ['secrets', 'SecretStore', 'Readiness probe completed · no secret values'],
    ['cache', 'State Cache', 'Source timestamps available'],
    ['queue', 'Queue pressure', 'Within the declared sampling budget'],
  ])
    add(
      id,
      'Management plane',
      label,
      'Synthetic management-plane probe',
      value,
      'Management plane / sw-edge-01',
      evidence,
    );
  const change = (
    id: string,
    status: HealthStatus,
    reason: string,
    impact: string,
    value = reason,
  ) => {
    const item = rows.find((row) => row.id === id)!;
    Object.assign(item, {
      status,
      reason,
      impact,
      value,
      code: `${id}.${reviewCase}`,
    });
  };
  if (reviewCase === 'normal') {
    change(
      'link',
      'Degraded',
      'The representative inventory reports server-08 down; intended administrative state is not established.',
      'Investigate this Port. Other links are not declared failed.',
      'Down observed · intent unknown',
    );
    change(
      'offload-provider',
      'Unknown',
      'The hardware provider has not supplied enough evidence.',
      'Hardware health and PF/VF mapping are unknown.',
      'No authoritative hardware health result',
    );
  }
  if (reviewCase === 'healthy') {
    const link = rows.find((item) => item.id === 'link')!;
    Object.assign(link, {
      related: 'Port/bond-storage',
      value: 'bond-storage member sample healthy',
      action: { label: 'Open related Bond', object: 'Port/bond-storage' },
    });
  }
  if (reviewCase === 'degraded')
    change(
      'telemetry',
      'Degraded',
      'The telemetry provider is returning partial observations.',
      'Counter coverage is incomplete; forwarding failure is not established.',
      'Partial telemetry',
    );
  if (reviewCase === 'critical') {
    change(
      'vswitchd',
      'Critical',
      'The synthetic process provider confirms ovs-vswitchd is not running.',
      'OVS configuration processing is unavailable. Existing packet forwarding is not established by this check.',
      'Process not running',
    );
    change(
      'packet-path',
      'Unknown',
      'No current packet-path confirmation accompanies the daemon failure.',
      'Do not infer either working or failed forwarding.',
      'Forwarding evidence unavailable',
    );
  }
  if (reviewCase === 'management-critical')
    change(
      'api',
      'Critical',
      'The API probe returned a confirmed failure.',
      'Management access is impaired. The independent datapath sample remains unchanged.',
      'API probe failed',
    );
  if (reviewCase === 'unknown')
    change(
      'offload-provider',
      'Unknown',
      'The provider returned no health facts.',
      'Absence of evidence does not establish Healthy, Down or Unsupported.',
      'No provider data',
    );
  if (reviewCase === 'recovering')
    change(
      'telemetry',
      'Recovering',
      'The provider resumed reporting; stabilization is not yet confirmed.',
      'Retain the incident history while confirming recovery.',
      'Recovery in progress',
    );
  if (reviewCase === 'recovery-required') {
    change(
      'manager-db',
      'Recovery Required',
      'The journal provider reports manager.db recovery is required.',
      'The management plane must remain in fail-safe review. This does not imply the datapath stopped.',
      'Recovery required · do not create an empty replacement database',
    );
    rows.find((item) => item.id === 'manager-db')!.action = {
      label: 'Inspect recovery evidence',
      view: 'evidence',
    };
  }
  return {
    id: `health-${now}`,
    capturedAt: now,
    generation: epoch,
    reviewCase,
    components: reviewCase === 'empty' ? [] : rows,
  };
}

export function deriveHealth(
  snapshot: HealthSnapshot | null,
  control: ControlState,
  signals: HealthSignals,
  now: number,
  failed = false,
) {
  if (control.scenario === 'permission-denied')
    return {
      components: [] as HealthComponent[],
      overall: 'Unknown' as HealthStatus,
      reason: 'Health access denied. Cached health observations are hidden.',
      unknown: 0,
      incidents: [] as HealthComponent[],
    };
  const historical =
    failed ||
    !!healthReadBlock(control.scenario) ||
    control.scenario === 'stale';
  const components = (
    control.scenario === 'empty' ? [] : (snapshot?.components ?? [])
  ).map((item) => ({ ...item }));
  const transient = (
    id: string,
    domain: HealthDomain,
    label: string,
    status: HealthStatus,
    reason: string,
    related: string,
    action: HealthAction,
  ): HealthComponent => ({
    id,
    domain,
    label,
    status,
    reason,
    impact:
      domain === 'Management plane'
        ? 'Management evidence does not determine datapath state.'
        : 'Inspect the linked resource for authoritative detail.',
    value: reason,
    code: id + '.' + status,
    source: 'Shared prototype resource',
    observedAt: now,
    since: now,
    generation: control.generation,
    freshness: 'Fresh',
    related,
    action,
  });
  if (!components.length) {
    const coverage = transient(
      'health-coverage',
      'Management plane',
      'Health observation coverage',
      'Unknown',
      'No health observations are available. Shared safety and Job resources are shown separately.',
      'Host / sw-edge-01',
      { label: 'Open shared evidence', view: 'evidence' },
    );
    coverage.observedAt = 0;
    coverage.since = 0;
    coverage.freshness = 'Unavailable';
    components.push(coverage);
  }
  const set = (
    id: string,
    status: HealthStatus,
    reason: string,
    action?: HealthAction,
  ) => {
    const item = components.find((row) => row.id === id);
    if (item)
      Object.assign(item, {
        status,
        reason,
        value: reason,
        code: `${id}.${control.scenario}`,
        ...(action ? { action } : {}),
      });
  };
  if (['degraded', 'provider-degraded'].includes(control.scenario))
    set(
      'telemetry',
      'Degraded',
      'The shared provider state reports degraded telemetry.',
    );
  if (control.scenario === 'member-down') {
    set(
      'link',
      'Degraded',
      'bond-storage has a down member Interface; forwarding capacity is reduced.',
      {
        label: 'Prepare member diagnostic',
        diagnostic: 'diag.net.link-lacp',
        scope: 'Port/bond-storage',
      },
    );
    const link = components.find((item) => item.id === 'link');
    if (link) link.related = 'Port/bond-storage';
  }
  if (control.scenario === 'lacp-mismatch') {
    set(
      'link',
      'Degraded',
      'bond-uplink has inconsistent LACP partner evidence.',
      {
        label: 'Prepare LACP diagnostic',
        diagnostic: 'diag.net.link-lacp',
        scope: 'Port/bond-uplink',
      },
    );
    const link = components.find((item) => item.id === 'link');
    if (link) link.related = 'Port/bond-uplink';
  }
  for (const row of components) {
    if (!row.observedAt) continue;
    if (
      historical ||
      now < row.observedAt ||
      now - row.observedAt >= healthTtlMs ||
      row.generation !== control.generation
    ) {
      row.historicalStatus = row.status;
      row.status = 'Unknown';
      row.freshness =
        historical && control.scenario !== 'stale' ? 'Unavailable' : 'Stale';
      row.reason =
        row.generation !== control.generation
          ? 'Observation belongs to a different instance generation.'
          : 'Current health is unconfirmed. Inspect the retained historical evidence and its source.';
    }
  }
  // Existing collectors retain their own scope, timestamps and failure semantics.
  for (const observed of signals.acceleration.snapshot?.records ?? []) {
    const record = projectAccelerationRecord(observed, control.scenario);
    const assessment = assessAcceleration(
      record,
      now,
      control.generation,
      signals.acceleration.failure ? 'provider-unavailable' : control.scenario,
    );
    const status: HealthStatus =
      assessment.state === 'Unknown' || record.provider === 'Unavailable'
        ? 'Unknown'
        : record.provider === 'Degraded' ||
            assessment.state === 'Missing prerequisites'
          ? 'Degraded'
          : 'Healthy';
    const row = transient(
      `${record.id}-provider`,
      'Providers',
      record.title,
      status,
      assessment.state + ' · ' + assessment.reason,
      record.scope,
      { label: 'Inspect acceleration evidence', view: 'acceleration-overview' },
    );
    row.source =
      'Shared acceleration snapshot / ' + signals.acceleration.snapshot!.id;
    row.observedAt = signals.acceleration.snapshot!.capturedAt;
    row.generation = signals.acceleration.snapshot!.generation;
    row.freshness =
      signals.acceleration.failure || healthReadBlock(control.scenario)
        ? 'Unavailable'
        : control.scenario === 'stale' ||
            now < row.observedAt ||
            now - row.observedAt >= healthTtlMs ||
            row.generation !== control.generation
          ? 'Stale'
          : 'Fresh';
    row.impact =
      'Capability readiness and provider coverage do not prove every Interface or flow healthy.';
    const index = components.findIndex((item) => item.id === row.id);
    if (index >= 0) components[index] = row;
    else components.push(row);
  }
  if (signals.openFlow.snapshot) {
    const flow = signals.openFlow.snapshot;
    const freshness =
      control.scenario === 'stale' || now < flow.observedAt
        ? 'Stale'
        : flowFreshness(flow, now);
    const unavailable =
      !!signals.openFlow.failure || !!healthReadBlock(control.scenario);
    const partial = flow.providerDegraded || flow.truncation.length > 0;
    const row = transient(
      'flow-collection',
      'Datapath',
      'OpenFlow collection',
      unavailable || freshness === 'Stale'
        ? 'Unknown'
        : partial
          ? 'Degraded'
          : 'Healthy',
      unavailable
        ? 'The current collection service is unavailable.'
        : freshness === 'Stale'
          ? 'The captured query has expired; refresh its original scope.'
          : partial
            ? 'The captured query has partial coverage.'
            : 'The bounded query completed; this is collection health, not a forwarding verdict.',
      `Bridge/${flow.query.bridge}`,
      { label: 'Inspect captured query', view: 'openflow-viewer' },
    );
    row.observedAt = flow.observedAt;
    // The accepted OpenFlow collector does not capture an instance generation.
    row.generation = null;
    row.source = 'Shared OpenFlow snapshot / ' + flow.id;
    row.freshness = unavailable ? 'Unavailable' : freshness;
    components.push(row);
  }
  const native = signals.capability?.rows.find((row) => row.id === 'protected');
  if (native) {
    const status =
      native.freshness !== 'Fresh' || native.providerState === 'Unavailable'
        ? 'Unknown'
        : native.providerState === 'Degraded'
          ? 'Degraded'
          : 'Healthy';
    const row = transient(
      'native-capability-provider',
      'Providers',
      'Native capability evidence',
      status,
      `${native.state} · ${native.reason}`,
      native.scope,
      { label: 'Inspect capability gates', view: 'capabilities' },
    );
    row.source = native.source;
    row.observedAt = native.observedAt;
    row.generation = native.generation;
    row.freshness =
      native.freshness === 'Fresh'
        ? 'Fresh'
        : native.freshness === 'Unavailable'
          ? 'Unavailable'
          : 'Stale';
    row.impact =
      'Provider evidence only. Availability does not authorize a native operation or prove forwarding health.';
    components.push(row);
  }
  const transaction = control.transaction;
  const recovery = [
    'outcome-unknown',
    'rollback-conflict',
    'degraded',
    'needs-attention',
  ].includes(transaction.status);
  const pending = transaction.status === 'countdown';
  const safety = transient(
    'change-safety',
    'Change safety',
    'Safe Apply / recovery',
    recovery
      ? 'Recovery Required'
      : pending ||
          ['drift', 'conflict', 'validation-blocked'].includes(control.scenario)
        ? 'Degraded'
        : 'Healthy',
    recovery
      ? `Transaction ${transaction.id} remains ${transaction.status}; inspect the original resource before any further change.`
      : pending
        ? 'An existing Safe Apply awaits confirmation. Health observations do not confirm it.'
        : control.scenario === 'drift'
          ? 'External configuration drift needs explicit reconciliation.'
          : ['conflict', 'validation-blocked'].includes(control.scenario)
            ? 'The Candidate requires review before another Apply.'
            : 'No unresolved transaction is reported by the shared resource.',
    transaction.id || 'Candidate workspace',
    {
      label: recovery
        ? 'Open transaction recovery'
        : pending
          ? 'Review pending Safe Apply'
          : 'Review workspace',
      view: recovery || pending ? 'safe-apply' : 'workspace',
    },
  );
  safety.source = 'Shared Candidate / transaction resource';
  safety.impact =
    'Configuration safety only; successful health reads never release a transaction lock.';
  safety.value = `${transaction.status} · checkpoint ${transaction.snapshot ? 'retained in the shared transaction' : 'not present'}${transaction.deadline ? ' · deadline ' + new Date(transaction.deadline).toISOString() : ''}`;
  components.push(safety);
  const diagnostic = signals.diagnostic;
  const failedJob =
    !diagnostic.reviewOnly &&
    [
      'failed',
      'partial',
      'truncated',
      'provider-unavailable',
      'command-failed',
      'evidence-unavailable',
    ].includes(diagnostic.state);
  const unknownJob =
    !diagnostic.reviewOnly &&
    ['expired', 'unavailable', 'no-data'].includes(diagnostic.state);
  components.push(
    transient(
      'diagnostic-job',
      'Jobs',
      'Bounded diagnostic Job',
      failedJob ? 'Degraded' : unknownJob ? 'Unknown' : 'Healthy',
      diagnostic.reviewOnly
        ? 'A result preview is open; it is not an executed Job.'
        : diagnostic.state === 'not-started'
          ? 'No diagnostic Job has been submitted this session.'
          : `Shared Job state: ${diagnostic.state}. Diagnostic completion does not prove system health.`,
      diagnostic.scope,
      {
        label: 'Open diagnostic Job',
        view:
          diagnostic.state === 'not-started'
            ? 'diagnostics-hub'
            : 'diagnostic-run',
      },
    ),
  );
  const incidents = components
    .filter((item) => item.status !== 'Healthy')
    .sort(
      (a, b) =>
        healthStates.indexOf(b.status) - healthStates.indexOf(a.status) ||
        a.id.localeCompare(b.id),
    );
  const overall = rollupHealth(components);
  return {
    components,
    overall,
    reason:
      incidents[0]?.reason ??
      'All checks in this bounded observation sample have current, consistent evidence.',
    unknown: components.filter((item) => item.status === 'Unknown').length,
    incidents,
  };
}

export function preserveHealthSince(
  previous: HealthComponent[],
  next: HealthComponent[],
  now: number,
) {
  return next.map((item) => {
    const before = previous.find(
      (row) =>
        row.id === item.id &&
        row.generation === item.generation &&
        row.related === item.related,
    );
    return before
      ? {
          ...item,
          since:
            before.status === item.status && before.since > 0
              ? before.since
              : now,
        }
      : item;
  });
}

export function healthEvents(
  previous: HealthComponent[],
  next: HealthComponent[],
  now: number,
): EvidenceEntry[] {
  return next.flatMap((item) => {
    const before = previous.find(
      (row) =>
        row.id === item.id &&
        row.related === item.related &&
        row.generation === item.generation,
    );
    if (!before || before.status === item.status) return [];
    return [
      {
        at: now,
        kind: 'Event' as const,
        text: `Health · ${item.label}: ${before.status} → ${item.status}. ${item.reason}`,
        object: item.related,
        correlation: `corr-health-${item.id}`,
      },
    ];
  });
}
