import type { StatisticRates, Statistics, StatisticSample } from '../../clients/typescript/public-v1.generated';

export const statisticsRoute = '/visibility/statistics';

export const statisticCounters = [
  ['rx_bytes', 'Received bytes', 'bytes'],
  ['tx_bytes', 'Transmitted bytes', 'bytes'],
  ['rx_packets', 'Received packets', 'packets'],
  ['tx_packets', 'Transmitted packets', 'packets'],
  ['rx_errors', 'Receive errors', 'errors'],
  ['tx_errors', 'Transmit errors', 'errors'],
  ['rx_dropped', 'Receive drops', 'packets'],
  ['tx_dropped', 'Transmit drops', 'packets'],
] as const;

export const statisticScopes = [
  ['bridge_id', 'Bridge ID'],
  ['port_id', 'Port ID'],
  ['interface_id', 'Interface ID'],
] as const;

export function statisticsPath(query: string, cursor?: string | null) {
  const params = new URLSearchParams(query);
  if (!params.has('limit')) params.set('limit', '25');
  if (cursor) params.set('cursor', cursor);
  else params.delete('cursor');
  return statisticsRoute + '?' + params;
}

export function counterValue(value: unknown): string {
  return typeof value === 'string' && /^(0|[1-9][0-9]{0,19})$/.test(value) ? value : 'Unknown';
}

export function rateValue(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return 'Unknown';
  if (value > 0 && value < 0.001) return '<0.001';
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 }).format(value);
}

export function rateFor(statistics: Statistics | undefined, key: string) {
  if (!statistics || statistics.source.freshness !== 'fresh' || statistics.state !== 'observed') return null;
  return key in statistics.rates ? statistics.rates[key as keyof StatisticRates] : null;
}

const discontinuities: Record<string, string> = {
  reset: 'Counter reset',
  'counter-reset': 'Counter reset',
  counter_reset: 'Counter reset',
  'first-sample': 'First sample',
  'initial-sample': 'First sample',
  'source-changed': 'Observation source changed',
  'identity-changed': 'Interface identity changed',
  'generation-changed': 'Provider restarted',
  'collection-gap': 'Collection gap',
  'sample-gap': 'Collection gap',
  'collector-gap': 'Collection gap',
  'association-changed': 'Interface attachment changed',
  'schema-changed': 'Native schema changed',
  'counter-unavailable': 'Some counters are unreported',
  'counter-missing': 'Some counters are unreported',
  'missing-counters': 'Some counters are unreported',
  'clock-discontinuity': 'Observation clock changed',
};

const reasons: Record<string, string> = {
  STATISTICS_RESYNCING: 'Waiting for the shared collector to establish a current sample.',
  STATISTICS_UNAVAILABLE: 'A current statistics sample is unavailable.',
  STATISTICS_SOURCE_CHANGED: 'The observation source changed. A new rate baseline is required.',
  STATISTICS_SCHEMA_UNSUPPORTED: 'Traffic statistics are unsupported by the current source.',
  STATISTICS_COUNTERS_UNREPORTED: 'Some traffic counters were unreported.',
  STATISTICS_OBSERVATION_EXPIRED: 'The retained sample has expired. Current traffic rates are unavailable.',
  OVSDB_UNAVAILABLE: 'The statistics source is disconnected.',
};

export function statisticsReason(code: string) {
  return reasons[code] ?? 'Statistics coverage is limited for this observation.';
}

export function sampleState(sample: Pick<StatisticSample, 'reset' | 'discontinuity' | 'coalesced'>) {
  const states: string[] = [];
  if (sample.reset) states.push('Counter reset');
  if (sample.discontinuity) {
    const label = discontinuities[sample.discontinuity.toLowerCase()] ?? 'Rate continuity interrupted';
    if (!states.includes(label)) states.push(label);
  }
  if (sample.coalesced) states.push('Collection slots skipped');
  return states.length ? states.join(' · ') : 'Continuous';
}
