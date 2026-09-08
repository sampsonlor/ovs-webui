export type P1SwitchingPage =
  | 'switching-overview'
  | 'bridges'
  | 'bridge-detail'
  | 'bonds'
  | 'bond-edit'
  | 'bond-detail';

export type P1DiagnosticsPage = 'diagnostics-hub' | 'diagnostic-run';

export type P1OpenFlowPage = 'openflow-viewer';
export type P1AccelerationPage = 'acceleration-overview';
export type P1HealthPage = 'system-health';

export type P1View =
  | P1SwitchingPage
  | P1DiagnosticsPage
  | P1OpenFlowPage
  | P1AccelerationPage
  | P1HealthPage;

export type OpenFlowReviewState =
  | 'fresh'
  | 'external-authority'
  | 'stale'
  | 'truncated'
  | 'empty'
  | 'provider-unavailable'
  | 'permission-denied'
  | 'query-timeout';

export const openFlowReviewLabels: Record<OpenFlowReviewState, string> = {
  fresh: 'Fresh bounded result',
  'external-authority': 'External authority',
  stale: 'Stale snapshot',
  truncated: 'Truncated result',
  empty: '0 rows',
  'provider-unavailable': 'Provider unavailable',
  'permission-denied': 'Permission denied',
  'query-timeout': 'Query timeout',
};

export type DiagnosticInputState =
  | 'valid'
  | 'validation-error'
  | 'scope-too-broad';

export type DiagnosticParameters = {
  sampleSeconds: 5 | 10 | 15;
  detail: 'structured' | 'bounded';
};

export type DiagnosticRequest = DiagnosticParameters & {
  id: string;
  scope: string;
};

export type DiagnosticJobState =
  | 'not-started'
  | 'queued'
  | 'running'
  | 'cancel-requested'
  | 'cancelled'
  | 'complete'
  | 'failed'
  | 'partial'
  | 'truncated'
  | 'expired'
  | 'unavailable'
  | 'no-finding'
  | 'no-data'
  | 'provider-unavailable'
  | 'command-failed'
  | 'evidence-unavailable';

export const diagnosticJobLabels: Record<DiagnosticJobState, string> = {
  'not-started': 'Not started',
  queued: 'Queued',
  running: 'Running',
  'cancel-requested': 'Cancel requested',
  cancelled: 'Cancelled',
  complete: 'Complete',
  failed: 'Failed',
  partial: 'Partial result',
  truncated: 'Truncated result',
  expired: 'Expired result',
  unavailable: 'Result unavailable',
  'no-finding': 'No finding',
  'no-data': 'No data',
  'provider-unavailable': 'Provider unavailable',
  'command-failed': 'Operation failed',
  'evidence-unavailable': 'Evidence unavailable',
};

export type PrototypeMode = 'standard' | 'expert';

export type ReviewScenario =
  | 'normal'
  | 'drift'
  | 'member-down'
  | 'lacp-mismatch'
  | 'validation-blocked'
  | 'provider-degraded'
  | 'advanced-config'
  | 'outcome-unknown'
  | 'network-loss';

export type ChangeIntent = {
  kind: 'vlan' | 'bridge' | 'bond' | 'isolation';
  objectType: 'Port' | 'Bridge';
  objectName: string;
  title: string;
  summary: string;
  current: string;
  candidate: string;
  risk: 'Low' | 'Medium' | 'High';
  capability: string;
  evidenceObject: string;
  bridgeName?: string;
};

export const p1Steps: Array<{ id: string; label: string; view: P1View }> = [
  { id: 'P1-01', label: 'Switching', view: 'switching-overview' },
  { id: 'P1-02', label: 'Bridges', view: 'bridges' },
  { id: 'P1-03', label: 'Bridge detail', view: 'bridge-detail' },
  { id: 'P1-04', label: 'Bond / LACP', view: 'bonds' },
  { id: 'P1-05', label: 'Create / edit', view: 'bond-edit' },
  { id: 'P1-06', label: 'Bond detail', view: 'bond-detail' },
  { id: 'P1-07', label: 'Diagnostics', view: 'diagnostics-hub' },
  { id: 'P1-08', label: 'Run & result', view: 'diagnostic-run' },
  { id: 'P1-09', label: 'OpenFlow', view: 'openflow-viewer' },
  { id: 'P1-10', label: 'DPDK / Offload', view: 'acceleration-overview' },
  { id: 'P1-11', label: 'System Health', view: 'system-health' },
];

export const p1SwitchingPages: P1SwitchingPage[] = [
  'switching-overview',
  'bridges',
  'bridge-detail',
  'bonds',
  'bond-edit',
  'bond-detail',
];

export const p1DiagnosticsPages: P1DiagnosticsPage[] = [
  'diagnostics-hub',
  'diagnostic-run',
];

export const p1OpenFlowPages: P1OpenFlowPage[] = ['openflow-viewer'];

export const p1Views: P1View[] = [
  ...p1SwitchingPages,
  ...p1DiagnosticsPages,
  ...p1OpenFlowPages,
  'acceleration-overview',
  'system-health',
];
