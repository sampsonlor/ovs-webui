import type { OpenFlowReviewState } from '../app/prototype-model';
import type { Scenario } from './change-control';

export const openFlowLimits = {
  rows: 500,
  bytes: 256 * 1024,
  timeoutMs: 5000,
  ttlMs: 30000,
};
export const openFlowSources = [
  {
    bridge: 'br-fabric',
    datapath: 'system',
    protocol: 'OpenFlow 1.5',
    authority: 'External controller',
    available: true,
    ports: ['1', '12', '13'],
  },
  {
    bridge: 'br-storage',
    datapath: 'system',
    protocol: 'OpenFlow 1.3',
    authority: 'External controller',
    available: true,
    ports: ['7'],
  },
  {
    bridge: 'br-offload',
    datapath: 'netdev',
    protocol: 'Unknown',
    authority: 'Unknown',
    available: false,
    ports: [],
  },
] as const;

export type FlowQuery = {
  bridge: string;
  table: string;
  priority: string;
  inPort: string;
  match: string;
  action: string;
  search: string;
};
export const defaultFlowQuery: FlowQuery = {
  bridge: 'br-fabric',
  table: 'all',
  priority: '',
  inPort: '',
  match: '',
  action: '',
  search: '',
};
export type FlowRow = {
  id: string;
  table: number;
  priority: number;
  cookie: string;
  match: string;
  action: string;
  packets: number;
  bytes: number;
  duration: number;
  inPort: string;
  relatedObject: string;
  raw: string;
};
export type FlowSnapshot = {
  id: string;
  query: FlowQuery;
  source: (typeof openFlowSources)[number];
  observedAt: number;
  elapsedMs: number;
  rows: FlowRow[];
  matchedRows: number;
  truncation: Array<'row-limit' | 'byte-limit'>;
  providerDegraded: boolean;
  reviewCase: OpenFlowReviewState;
};
export type FlowFailure =
  | 'permission-denied'
  | 'provider-unavailable'
  | 'query-timeout';

export function flowQueryErrors(query: FlowQuery) {
  const errors: Partial<Record<keyof FlowQuery, string>> = {};
  const source = openFlowSources.find((item) => item.bridge === query.bridge);
  if (!source) errors.bridge = 'Choose a Bridge in the collection catalog.';
  if (!['all', '0', '10', '20', '30', '90'].includes(query.table))
    errors.table = 'Choose a supported table filter.';
  if (
    query.priority &&
    (!/^\d{1,5}$/.test(query.priority) || Number(query.priority) > 65535)
  )
    errors.priority = 'Use an integer from 0 to 65535, or leave blank.';
  if (query.inPort && !source?.ports.some((port) => port === query.inPort))
    errors.inPort = 'Choose an in_port from this Bridge sample.';
  for (const key of ['match', 'action', 'search'] as const) {
    if (
      query[key].length > 128 ||
      Array.from(query[key]).some(
        (character) =>
          character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127,
      )
    )
      errors[key] = 'Use up to 128 visible characters.';
  }
  return errors;
}

export function captureFlowQuery(query: FlowQuery): FlowQuery {
  if (Object.keys(flowQueryErrors(query)).length)
    throw new Error('Correct the query fields before collecting.');
  return { ...query };
}

export function flowServiceBlock(scenario: Scenario): string | null {
  const messages: Partial<Record<Scenario, string>> = {
    loading: 'Collection catalog is loading.',
    empty: 'No Bridges are available in this catalog.',
    error: 'Inventory could not be read. Collection is unavailable.',
    'permission-denied':
      'Permission denied. Captured rows and exports are withheld.',
    'provider-unavailable': 'OpenFlow provider is unavailable.',
    'network-loss': 'Connection lost. A new snapshot cannot be collected.',
  };
  return messages[scenario] ?? null;
}

export function flowAdmission(
  query: FlowQuery,
  scenario: Scenario,
  width: number,
  busy: boolean,
) {
  if (width < 768) return 'OpenFlow collection requires tablet or desktop.';
  if (busy) return 'Wait for the active collection to finish.';
  return (
    flowServiceBlock(scenario) ??
    (Object.keys(flowQueryErrors(query)).length
      ? 'Correct the query fields before collecting.'
      : null)
  );
}

function row(
  bridge: string,
  index: number,
  table: number,
  priority: number,
  match: string,
  action: string,
  inPort: string,
  relatedObject: string,
): FlowRow {
  const cookie = `0x${(0xa100 + index).toString(16).padStart(8, '0')}`;
  const packets = 18400 + index * 731;
  const bytes = packets * 512;
  const duration = 720 + index * 13.25;
  return {
    id: `${bridge}-flow-${index}`,
    table,
    priority,
    cookie,
    match,
    action,
    inPort,
    relatedObject,
    packets,
    bytes,
    duration,
    raw: `cookie=${cookie}, duration=${duration}s, table=${table}, n_packets=${packets}, n_bytes=${bytes}, priority=${priority}${match ? ',' + match : ''} actions=${action}`,
  };
}

function fixtures(bridge: string, large: boolean): FlowRow[] {
  const fabric = [
    [0, 200, 'in_port=1,dl_vlan=120', 'resubmit(,10)', '1', 'Port/uplink-01'],
    [0, 180, 'in_port=12,dl_vlan=120', 'resubmit(,20)', '12', 'Port/server-07'],
    [10, 150, 'dl_dst=02:00:00:00:4c:21', 'output:12', '', 'Port/server-07'],
    [10, 150, 'dl_dst=02:00:00:00:4c:22', 'output:13', '', 'Port/server-08'],
    [20, 110, 'ip,nw_dst=10.120.7.0/24', 'resubmit(,30)', '', 'Port/server-07'],
    [20, 100, 'arp,arp_tpa=10.120.7.1', 'NORMAL', '', 'Bridge/br-fabric'],
    [30, 90, 'ip,ct_state=+new', 'ct(commit,table=31)', '', 'Port/server-07'],
    [90, 0, '', 'drop', '', 'Bridge/br-fabric'],
  ] as const;
  const storage = [
    [
      0,
      180,
      'in_port=7,dl_vlan=300',
      'resubmit(,10)',
      '7',
      'Port/bond-storage',
    ],
    [10, 100, 'arp,arp_tpa=10.30.0.1', 'NORMAL', '', 'Bridge/br-storage'],
    [90, 0, '', 'drop', '', 'Bridge/br-storage'],
  ] as const;
  const templates = bridge === 'br-storage' ? storage : fabric;
  return Array.from({ length: large ? 768 : templates.length }, (_, index) => {
    const [table, priority, match, action, inPort, target] =
      templates[index % templates.length];
    const distinctMatch = large
      ? `${match ? match + ',' : ''}metadata=0x${index.toString(16)}`
      : match;
    return row(
      bridge,
      index,
      table,
      priority,
      distinctMatch,
      action,
      inPort,
      target,
    );
  });
}

export function flowFreshness(snapshot: FlowSnapshot, now: number) {
  return now - snapshot.observedAt >= openFlowLimits.ttlMs ? 'Stale' : 'Fresh';
}

export function serializeFlowSnapshot(
  snapshot: FlowSnapshot,
  now: number,
  providerDegraded = false,
  collectionUnavailable = false,
) {
  return JSON.stringify({
    schema: 'ovs-webui.openflow-snapshot.v1',
    synthetic: true,
    capability: 'Observe',
    exportedAt: new Date(now).toISOString(),
    freshness: flowFreshness(snapshot, now),
    coverage:
      snapshot.truncation.length ||
      snapshot.providerDegraded ||
      providerDegraded ||
      collectionUnavailable
        ? 'Partial'
        : 'Complete for captured query',
    currentProviderDegraded: providerDegraded,
    currentCollectionUnavailable: collectionUnavailable,
    limits: openFlowLimits,
    snapshot,
  });
}

export function flowOutputBytes(snapshot: FlowSnapshot, now: number) {
  return new TextEncoder().encode(serializeFlowSnapshot(snapshot, now))
    .byteLength;
}

export function collectFlowSnapshot(
  query: FlowQuery,
  reviewCase: OpenFlowReviewState,
  now: number,
  elapsedMs = 450,
  degraded = false,
): { snapshot: FlowSnapshot | null; failure: FlowFailure | null } {
  const captured = captureFlowQuery(query);
  const source = openFlowSources.find((item) => item.bridge === query.bridge)!;
  if (reviewCase === 'permission-denied')
    return { snapshot: null, failure: 'permission-denied' };
  if (!source.available || reviewCase === 'provider-unavailable')
    return { snapshot: null, failure: 'provider-unavailable' };
  if (reviewCase === 'query-timeout' || elapsedMs >= openFlowLimits.timeoutMs)
    return { snapshot: null, failure: 'query-timeout' };
  const rows = (
    reviewCase === 'empty'
      ? []
      : fixtures(query.bridge, reviewCase === 'truncated')
  ).filter(
    (flow) =>
      (query.table === 'all' || flow.table === Number(query.table)) &&
      (!query.priority || flow.priority >= Number(query.priority)) &&
      (!query.inPort || flow.inPort === query.inPort) &&
      flow.match.toLowerCase().includes(query.match.toLowerCase()) &&
      flow.action.toLowerCase().includes(query.action.toLowerCase()) &&
      `${flow.cookie} ${flow.match} ${flow.action}`
        .toLowerCase()
        .includes(query.search.toLowerCase()),
  );
  const snapshot: FlowSnapshot = {
    id: `ofs-${query.bridge}-${now}`,
    query: captured,
    source,
    observedAt: now - (reviewCase === 'stale' ? 45000 : 0),
    elapsedMs,
    rows: rows.slice(0, openFlowLimits.rows),
    matchedRows: rows.length,
    truncation: rows.length > openFlowLimits.rows ? ['row-limit'] : [],
    providerDegraded: degraded,
    reviewCase,
  };
  return { snapshot: boundFlowSnapshot(snapshot, now), failure: null };
}

export function boundFlowSnapshot(input: FlowSnapshot, now: number) {
  const snapshot: FlowSnapshot = {
    ...input,
    rows: input.rows.slice(0, openFlowLimits.rows),
    truncation: [...input.truncation],
  };
  if (
    input.rows.length > openFlowLimits.rows &&
    !snapshot.truncation.includes('row-limit')
  )
    snapshot.truncation.push('row-limit');
  // Reserve metadata space for later freshness/provider flags without exceeding the export ceiling.
  if (flowOutputBytes(snapshot, now) > openFlowLimits.bytes - 1024) {
    if (!snapshot.truncation.includes('byte-limit'))
      snapshot.truncation.push('byte-limit');
    const cappedRows = snapshot.rows;
    let low = 0;
    let high = cappedRows.length;
    while (low < high) {
      const mid = Math.ceil((low + high) / 2);
      snapshot.rows = cappedRows.slice(0, mid);
      if (flowOutputBytes(snapshot, now) <= openFlowLimits.bytes - 1024)
        low = mid;
      else high = mid - 1;
    }
    snapshot.rows = cappedRows.slice(0, low);
  }
  return snapshot;
}

export function flowPage(
  rows: FlowRow[],
  requested: number,
  selectedId: string | null,
) {
  const pageCount = Math.max(1, Math.ceil(rows.length / 4));
  const page = Math.min(pageCount, Math.max(1, requested));
  const visible = rows.slice((page - 1) * 4, page * 4);
  return {
    page,
    pageCount,
    visible,
    selected:
      visible.find((flow) => flow.id === selectedId) ?? visible[0] ?? null,
  };
}
