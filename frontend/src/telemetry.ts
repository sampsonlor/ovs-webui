import type { TelemetryExporterClearIntent } from '../../clients/typescript/public-v1.generated';

export type TelemetryProtocol = TelemetryExporterClearIntent['protocol'];

export const telemetryLabels: Record<TelemetryProtocol, string> = {
  sflow: 'sFlow',
  netflow: 'NetFlow',
  ipfix: 'IPFIX',
};

export const telemetrySettingLabels = {
  sampling: 'Sampling rate',
  polling: 'Polling interval (s)',
  active_timeout: 'Active timeout (s)',
  obs_domain_id: 'Observation domain ID',
  obs_point_id: 'Observation point ID',
};

type SettingKey = keyof typeof telemetrySettingLabels;
type Setting = { key: SettingKey; min: number; max: number; optional: boolean };

export const telemetrySettings: Record<TelemetryProtocol, Setting[]> = {
  sflow: [
    { key: 'sampling', min: 1, max: 4294967295, optional: true },
    { key: 'polling', min: 0, max: 3600, optional: true },
  ],
  netflow: [{ key: 'active_timeout', min: -1, max: 86400, optional: false }],
  ipfix: [
    { key: 'sampling', min: 1, max: 4294967295, optional: true },
    { key: 'obs_domain_id', min: 0, max: 4294967295, optional: true },
    { key: 'obs_point_id', min: 0, max: 4294967295, optional: true },
  ],
};

export const telemetryDiffLabels: Record<string, string> = {
  'sflow exporter': 'sFlow exporter',
  'netflow exporter': 'NetFlow exporter',
  'ipfix exporter': 'IPFIX exporter',
  ...telemetrySettingLabels,
};

export function isTelemetryOperation(operation?: string) {
  return operation === 'telemetry.exporter.set' || operation === 'telemetry.exporter.clear';
}

const telemetryMessages: Record<string, string> = {
  TELEMETRY_EXPORTER_SAFE_APPLY: 'Exporter change is eligible for Safe Apply.',
  TELEMETRY_WRITE_AVAILABLE: 'Exporter configuration is available.',
  TELEMETRY_WRITE_AUTHORITY_REQUIRED: 'Exporter changes require a write grant for this Bridge.',
  TELEMETRY_WRITE_SCHEMA_UNSUPPORTED: 'The current OVS schema does not support protected changes to this exporter.',
  TELEMETRY_REFERENCE_SHARED: 'This exporter is referenced by multiple native objects. It can be reviewed here.',
  TELEMETRY_CONFIGURATION_UNKNOWN: 'The current exporter configuration could not be verified.',
  TELEMETRY_BRIDGE_UNSUPPORTED: 'This Bridge is outside the supported exporter configuration scope.',
  TELEMETRY_BRIDGE_DEPENDENCY_CHANGED: 'The Bridge configuration changed. Review the current Bridge and stage the exporter change again.',
  TELEMETRY_CONFIGURATION_CHANGED: 'The exporter configuration changed. Review its current values and stage the change again.',
  TELEMETRY_CONFIGURATION_UNCHANGED: 'Change a collector target or setting to continue.',
  TELEMETRY_RESTAGE_REQUIRED: 'Discard the current intent and review the exporter again.',
  INVALID_TELEMETRY_TARGETS: 'Enter 1 to 16 unique numeric unicast IP addresses with UDP ports.',
  INVALID_TELEMETRY_SETTINGS: 'Review the basic settings for this telemetry protocol.',
  INVALID_TELEMETRY_CLEAR: 'Only a currently configured exporter can be disabled.',
  EXTERNALLY_CONTROLLED: 'This Bridge is managed by an external controller.',
  CAPABILITY_DENIED: 'Your current session cannot change Bridge exporters.',
  OVS_ASSOCIATION_STALE: 'Refresh telemetry and review the current Bridge identity.',
};

export function telemetryMessage(code: string, fallback = code) {
  return telemetryMessages[code] ?? fallback;
}

export function collectorTargetKey(target: string) {
  const match = /^(\[[0-9a-fA-F:.]+\]|\d{1,3}(?:\.\d{1,3}){3}):(\d{1,5})$/.exec(target);
  if (!match) return null;
  const [, address, portText] = match;
  let host = address;
  const port = Number(portText);
  if (port < 1 || port > 65535) return null;
  if (host.startsWith('[')) {
    try {
      const canonical = new URL('http://' + host).hostname;
      if (canonical === '[::]' || /^\[(ff|fe[89ab])/.test(canonical)) return null;
      const mapped = /^\[::ffff:([0-9a-f]+):([0-9a-f]+)\]$/.exec(canonical);
      if (!mapped) return `${canonical}:${port}`;
      const high = parseInt(mapped[1], 16);
      const low = parseInt(mapped[2], 16);
      host = [high >> 8, high & 255, low >> 8, low & 255].join('.');
    } catch {
      return null;
    }
  }
  const parts = host.split('.');
  if (parts.some(part => String(Number(part)) !== part || Number(part) > 255)) return null;
  const first = Number(parts[0]);
  if ((first >= 224 && first < 240) || host.startsWith('169.254.') || host === '0.0.0.0' || host === '255.255.255.255') return null;
  return `${host}:${port}`;
}
