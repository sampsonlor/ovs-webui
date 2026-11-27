export const healthRoute = '/operations/health';
export const capabilitiesRoute = '/admin/capabilities';

const managementID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function capabilityPath(id: string): string | null {
  return managementID.test(id) ? capabilitiesRoute + '/' + id : null;
}

const componentNames = new Map([
  ['web', 'Web service'],
  ['webd', 'Web service'],
  ['manager', 'Management service'],
  ['mgrd', 'Management service'],
  ['web-db', 'Web database'],
  ['manager-db', 'Management database'],
  ['web-secret-store', 'Web secret store'],
  ['manager-secret-store', 'Management secret store'],
  ['manager-ipc', 'Manager connection'],
  ['auth', 'Authentication'],
  ['tls', 'HTTPS'],
  ['ovsdb', 'OVSDB provider'],
  ['cache-interface-devices', 'Linux device observations'],
  ['cache-statistics', 'Interface statistics'],
  ['cache-neighbors', 'ARP and NDP observations'],
  ['cache-lldp', 'LLDP observations'],
  ['cache-fdb', 'FDB observations'],
  ['safety-watchdog', 'Safe Apply watchdog'],
  ['ipc-auth', 'Authentication queue'],
  ['ipc-control', 'Control queue'],
  ['ipc-read', 'Read queue'],
  ['ipc-heavy', 'Background work queue'],
  ['ipc-observation', 'Observation queue'],
  ['openflow-provider', 'OpenFlow provider'],
  ['dpdk-provider', 'DPDK provider'],
  ['offload-provider', 'Hardware offload provider'],
  ['diagnostics-executor', 'Diagnostics executor'],
  ['lifecycle-executor', 'OVS lifecycle executor'],
  ['hardware-runtime-evidence', 'Hardware runtime evidence'],
  ['target-authority', 'Target authority'],
  ['candidate-validation', 'Candidate validation'],
]);

const reasons = new Map([
  ['OVSDB_RESYNCING', 'Waiting for a current OVSDB observation.'],
  ['OVSDB_UNAVAILABLE', 'The OVSDB provider is unavailable.'],
  ['SAFETY_WATCHDOG_STALE', 'A current watchdog observation is unavailable.'],
  ['IPC_QUEUE_PRESSURE', 'Requests are waiting for available capacity.'],
  ['OPENFLOW_PROVIDER_UNAVAILABLE', 'OpenFlow observations are unavailable.'],
  ['DPDK_PROVIDER_UNAVAILABLE', 'DPDK observations are unavailable.'],
  [
    'OFFLOAD_PROVIDER_UNAVAILABLE',
    'Hardware offload observations are unavailable.',
  ],
  [
    'DIAGNOSTICS_EXECUTOR_UNAVAILABLE',
    'The diagnostics executor is unavailable.',
  ],
  [
    'LIFECYCLE_EXECUTOR_UNAVAILABLE',
    'OVS lifecycle operations are unavailable.',
  ],
  ['SAFE_APPLY_UNAVAILABLE', 'The Safe Apply recovery path is unavailable.'],
  [
    'TARGET_AUTHORITY_REQUIRED',
    'The target needs an explicit authority grant.',
  ],
  [
    'PREREQUISITE_EVIDENCE_UNAVAILABLE',
    'Current evidence for a prerequisite is unavailable.',
  ],
]);

export function observationLabel(value: string): string {
  const words = value.replaceAll('-', ' ').replaceAll('_', ' ').toLowerCase();
  return words ? words[0].toUpperCase() + words.slice(1) : 'Unknown';
}
export function componentName(key: string): string {
  return componentNames.get(key) ?? observationLabel(key);
}
export function observationReason(reason: string): string {
  return reasons.get(reason) ?? observationLabel(reason);
}
