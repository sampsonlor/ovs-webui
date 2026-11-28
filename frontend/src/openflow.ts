import type { OpenFlowReadPage, OpenFlowReadQuery, OpenFlowReadRow, Session } from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';

export const openFlowRoute = '/switching/openflow';
export const openFlowMaxRows = 128;
export const openFlowPageSize = 25;
const maxCookie = 18_446_744_073_709_551_615n;
const cookieEvidence = /^0x[0-9a-f]{16}$/;
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

type Observation = { bridge: string; table: number; limit: number; cookie: string; groups: boolean; rawFields?: boolean };
type Location = { kind: 'inventory'; filter: string; cursor: string } |
  { kind: 'snapshot'; id: string; offset: number } | { kind: 'invalid' };

export function canReadOpenFlow(session: Session | null): boolean {
  return has(session, 'state.read') && has(session, 'inventory.read');
}

export function canReadOpenFlowRawFields(session: Session | null): boolean {
  return canReadOpenFlow(session) && has(session, 'configuration.read');
}

export function openFlowLocation(query: string): Location {
  const params = new URLSearchParams(query);
  if (!params.has('snapshot')) return { kind: 'inventory', filter: (params.get('filter') ?? '').slice(0, 128), cursor: params.get('cursor') ?? '' };
  const id = params.get('snapshot')!;
  const offset = params.get('offset') ?? '0';
  if (!idPattern.test(id) || !/^(?:0|[1-9]\d{0,2})$/.test(offset) || Number(offset) > openFlowMaxRows ||
      params.getAll('snapshot').length !== 1 || params.getAll('offset').length > 1) return { kind: 'invalid' };
  return { kind: 'snapshot', id, offset: Number(offset) };
}

export function openFlowBridgePage(filter: string, cursor = ''): string {
  const query = new URLSearchParams({ limit: '25' });
  if (filter) query.set('filter', filter);
  if (cursor) query.set('cursor', cursor);
  return `/bridges?${query}`;
}

export function openFlowInventoryRoute(filter: string, cursor = ''): string {
  const query = new URLSearchParams();
  if (filter) query.set('filter', filter);
  if (cursor) query.set('cursor', cursor);
  return openFlowRoute + (query.size ? `?${query}` : '');
}

export function openFlowObservationValid(value: Observation): boolean {
  return idPattern.test(value.bridge) && Number.isInteger(value.table) && value.table >= 0 && value.table <= 254 &&
    Number.isInteger(value.limit) && value.limit >= 1 && value.limit <= openFlowMaxRows &&
    (value.cookie === '' || /^(?:0|[1-9]\d{0,19})$/.test(value.cookie) && BigInt(value.cookie) <= maxCookie);
}

export function openFlowObservationPath(value: Observation): string | null {
  if (!openFlowObservationValid(value)) return null;
  const query = new URLSearchParams({ table: String(value.table), row_limit: String(value.limit) });
  if (value.cookie) query.set('cookie', value.cookie);
  if (value.groups) query.set('groups_meters', 'true');
  if (value.rawFields === true) query.set('raw_fields', 'true');
  return `/openflow/bridges/${value.bridge}/snapshots?${query}`;
}

export function openFlowSnapshotRoute(id: string, offset = 0): string {
  return `${openFlowRoute}?${new URLSearchParams({ snapshot: id, offset: String(offset) })}`;
}

export function openFlowSnapshotPath(id: string, offset: number | 'export'): string {
  const query = offset === 'export' ? new URLSearchParams({ export: 'true' }) :
    new URLSearchParams({ offset: String(offset), page_size: String(openFlowPageSize) });
  return `/openflow/snapshots/${encodeURIComponent(id)}?${query}`;
}

function queryMatches(a: OpenFlowReadQuery, b: OpenFlowReadQuery): boolean {
  return a.table === b.table && (a.cookie ?? '') === (b.cookie ?? '') && a.limit === b.limit &&
    !!a.include_groups_meters === !!b.include_groups_meters && (a.raw_fields === true) === (b.raw_fields === true);
}

function validRawPayload(value: unknown, allowed: boolean, bytes: number, maxBytes: number): boolean {
  if (!allowed) return value === undefined;
  if (!Number.isInteger(bytes) || bytes < 1) return false;
  if (bytes > maxBytes) return value === undefined;
  return typeof value === 'string' && value.length === 2 * bytes && /^[0-9a-f]+$/.test(value);
}

function validFlowFields(row: OpenFlowReadRow, rawFields: boolean): boolean {
  return Array.isArray(row?.match_fields) && Array.isArray(row.instructions) &&
    row.match_fields.every((field) => !!field && validRawPayload(field.raw_payload_hex, rawFields, field.payload_bytes, 255)) &&
    row.instructions.every((instruction) => !!instruction && Number.isInteger(instruction.bytes) &&
      (!rawFields || instruction.bytes - 4 <= 256 || row.metadata_truncated) &&
      validRawPayload(instruction.raw_payload_hex, rawFields, instruction.bytes - 4, 256));
}

export function openFlowSnapshotAvailable(page: OpenFlowReadPage, now = Date.now()): boolean {
  const expiry = Date.parse(page.snapshot.expires_at);
  return Number.isFinite(expiry) && expiry > now;
}

export function validOpenFlowPage(page: OpenFlowReadPage | null, id: string, offset: number, full = false): page is OpenFlowReadPage {
  if (!page?.snapshot || !page.evidence) return false;
  const { snapshot, evidence } = page;
  const expiry = Date.parse(snapshot.expires_at);
  const observed = Date.parse(snapshot.observed_at);
  const rawFields = evidence.query?.raw_fields;
  if (!idPattern.test(id) || snapshot.id !== id || !idPattern.test(snapshot.bridge_id) || !idPattern.test(snapshot.instance_generation) ||
      snapshot.bridge_id !== evidence.bridge_id || snapshot.instance_generation !== evidence.instance_generation ||
      snapshot.observed_at !== evidence.observed_at || !Number.isFinite(observed) || !Number.isFinite(expiry) || expiry <= observed ||
      evidence.source !== 'openflow-1.3-unix' || evidence.ownership !== 'unknown' ||
      (rawFields !== undefined && typeof rawFields !== 'boolean') ||
      evidence.field_coverage !== (rawFields === true ? 'bounded-raw-fields' : 'metadata-only') ||
      snapshot.truncated !== evidence.truncated || !Array.isArray(evidence.rows) || !evidence.query ||
      (evidence.query.cookie !== undefined && !cookieEvidence.test(evidence.query.cookie)) ||
      !openFlowObservationValid({ bridge: snapshot.bridge_id, table: evidence.query.table ?? -1, limit: evidence.query.limit,
        cookie: '', groups: !!evidence.query.include_groups_meters }) ||
      !Number.isInteger(snapshot.retained_rows) || snapshot.retained_rows < 0 || snapshot.retained_rows > evidence.query.limit ||
      page.offset !== offset || offset < 0 || offset > snapshot.retained_rows) return false;

  if (!evidence.rows.every((row) => validFlowFields(row, rawFields === true))) return false;
  if (full) return offset === 0 && evidence.rows.length === snapshot.retained_rows && page.next_offset === undefined;
  const count = Math.min(openFlowPageSize, snapshot.retained_rows - offset);
  const next = offset + count < snapshot.retained_rows ? offset + count : undefined;
  return evidence.rows.length === count && page.next_offset === next;
}

export function matchesOpenFlowObservation(page: OpenFlowReadPage, scope: Observation): boolean {
  if (!openFlowObservationValid(scope)) return false;
  const cookie = scope.cookie ? `0x${BigInt(scope.cookie).toString(16).padStart(16, '0')}` : '';
  return validOpenFlowPage(page, page?.snapshot?.id ?? '', 0) && page.snapshot.bridge_id === scope.bridge &&
    queryMatches(page.evidence.query, { table: scope.table, cookie, limit: scope.limit, include_groups_meters: scope.groups,
      raw_fields: scope.rawFields });
}

export function matchesOpenFlowExport(exported: OpenFlowReadPage, original: OpenFlowReadPage): boolean {
  const before = original.snapshot;
  if (!validOpenFlowPage(exported, before.id, 0, true)) return false;
  const after = exported.snapshot;
  return after.bridge_id === before.bridge_id && after.instance_generation === before.instance_generation &&
    after.observed_at === before.observed_at && after.expires_at === before.expires_at &&
    after.retained_rows === before.retained_rows && after.truncated === before.truncated &&
    queryMatches(exported.evidence.query, original.evidence.query) &&
    JSON.stringify(exported.evidence.rows.slice(original.offset, original.offset + original.evidence.rows.length)) ===
      JSON.stringify(original.evidence.rows) &&
    JSON.stringify(exported.evidence.group_rows) === JSON.stringify(original.evidence.group_rows) &&
    JSON.stringify(exported.evidence.meter_rows) === JSON.stringify(original.evidence.meter_rows);
}

const reasons = new Map([
  ['OPENFLOW_SNAPSHOT_UNAVAILABLE', 'This OpenFlow snapshot is unavailable or expired. Observe again to create a new snapshot.'],
  ['OPENFLOW_SCOPE_CHANGED', 'The Bridge instance changed. Observe its current scope again.'],
  ['OPENFLOW_AUTHORITY_CHANGED', 'OpenFlow permissions changed. Refresh the session and scope.'],
  ['OPENFLOW_SOURCE_UNAVAILABLE', 'This Bridge has no available qualified OpenFlow source.'],
  ['OPENFLOW_INVENTORY_UNAVAILABLE', 'Current Bridge identity could not be verified.'],
  ['OPENFLOW_SNAPSHOT_CAPACITY', 'The snapshot limit has been reached. Wait for a retained snapshot to expire.'],
  ['INVALID_OPENFLOW_PAGE', 'This page is outside the retained snapshot.'],
  ['OPENFLOW_VIEW_MISMATCH', 'The response does not match the requested snapshot. Observe the Bridge again.'],
]);

export function openFlowReason(code: string): string {
  return reasons.get(code) ?? 'Current OpenFlow evidence is unavailable.';
}
