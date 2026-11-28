import type {
  InterfaceCounterDiagnosticResult,
  ResourceRef,
  Session,
  Source,
} from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';

export const diagnosticsRoute = '/operations/diagnostics';
export const linkLACPPath = '/diagnostics/link-lacp';
export const linkLACPOperation = 'runLinkLACPDiagnostic';
export const interfaceCounterPath = '/diagnostics/interface-counters';
export const interfaceCounterOperation = 'runInterfaceCounterDiagnostic';
export type DiagnosticTemplate = 'link-lacp' | 'interface-counters';
export type DiagnosticTargetKind = 'bridge' | 'port';

const managementID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
type Target = { ref: ResourceRef; name: string };
type InventoryPage = {
  source?: Source;
  availability?: string;
  items?: Array<{ id?: unknown; management_id?: unknown; name?: unknown }>;
};

export function canObserveDiagnostics(session: Session | null): boolean {
  return has(session, 'diagnostics.read') && has(session, 'inventory.read');
}

export function canSampleInterfaceCounters(session: Session | null): boolean {
  return has(session, 'diagnostics.active') && has(session, 'inventory.read');
}

export function diagnosticTemplate(
  query: string,
  session: Session | null,
): DiagnosticTemplate {
  const template = new URLSearchParams(query).get('template');
  if (template === 'interface-counters' || template === 'link-lacp')
    return template;
  return canSampleInterfaceCounters(session) && !canObserveDiagnostics(session)
    ? 'interface-counters'
    : 'link-lacp';
}

export function diagnosticResultID(query: string): string | null {
  const id = new URLSearchParams(query).get('job_id');
  return id && managementID.test(id) ? id : null;
}

export function diagnosticResultRoute(
  id: string,
  session: Session | null,
): string | null {
  if (!managementID.test(id)) return null;
  if (has(session, 'jobs.read')) return `/operations/jobs/${id}`;
  return `${diagnosticsRoute}?job_id=${id}`;
}

export function diagnosticTargetQuery(
  query: string,
  template: DiagnosticTemplate = 'link-lacp',
) {
  const values = new URLSearchParams(query);
  const kind: DiagnosticTargetKind =
    template === 'interface-counters' || values.get('kind') === 'port'
      ? 'port'
      : 'bridge';
  const filter = (values.get('filter') ?? '').slice(0, 128);
  const cursor = values.get('cursor') ?? '';
  const inventory = new URLSearchParams({ limit: '25' });
  if (filter) inventory.set('filter', filter);
  if (cursor) inventory.set('cursor', cursor);
  return {
    kind,
    filter,
    cursor,
    inventory: `/${kind === 'port' ? 'ports' : 'bridges'}?${inventory}`,
  };
}

export function diagnosticTargets(
  value: InventoryPage | null,
  kind: DiagnosticTargetKind,
): Target[] | null {
  if (
    value?.source?.freshness !== 'fresh' ||
    value.availability !== 'complete' ||
    !Array.isArray(value.items)
  )
    return null;
  const targets: Target[] = [];
  for (const item of value.items) {
    const id = kind === 'port' ? item.management_id : item.id;
    if (
      typeof id === 'string' &&
      managementID.test(id) &&
      typeof item.name === 'string' &&
      item.name
    )
      targets.push({ ref: { kind, id }, name: item.name });
  }
  return targets;
}

export function isLinkLACPJob(
  job: Record<string, unknown> | null,
  session: Session | null,
): boolean {
  return (
    !!session &&
    job?.operation === linkLACPOperation &&
    job.owner_id === session.principal_id &&
    typeof job.id === 'string' &&
    managementID.test(job.id)
  );
}

function refID(value: unknown, kind: string): string | null {
  if (!value || typeof value !== 'object') return null;
  const ref = value as Partial<ResourceRef>;
  return ref.kind === kind && typeof ref.id === 'string' && managementID.test(ref.id)
    ? ref.id
    : null;
}

export function interfaceCounterPortID(value: unknown): string | null {
  return refID(value, 'port');
}

export function interfaceCounterJobPortID(
  job: Record<string, unknown> | null,
  session: Session | null,
): string | null {
  return session &&
    job?.operation === interfaceCounterOperation &&
    job.owner_id === session.principal_id &&
    typeof job.id === 'string' &&
    managementID.test(job.id)
    ? interfaceCounterPortID(job.resource_ref)
    : null;
}

export function interfaceCounterResultRoute(
  jobID: string,
  portID: string,
  session: Session | null,
): string | null {
  if (!managementID.test(jobID) || !managementID.test(portID)) return null;
  if (has(session, 'jobs.read')) return `/operations/jobs/${jobID}`;
  const query = new URLSearchParams({
    template: 'interface-counters',
    job_id: jobID,
    target_id: portID,
  });
  return `${diagnosticsRoute}?${query}`;
}

export function interfaceCounterResultTarget(query: string): string | null {
  return interfaceCounterPortID({
    kind: 'port',
    id: new URLSearchParams(query).get('target_id'),
  });
}

export function matchesInterfaceCounterResult(
  result: InterfaceCounterDiagnosticResult | null,
  portID: string,
): boolean {
  if (
    !result ||
    result.template_id !== 'diag.host.interface-counters' ||
    interfaceCounterPortID(result.target_ref) !== portID ||
    !Array.isArray(result.members) ||
    result.members.length > 4
  )
    return false;
  const seen = new Set<string>();
  for (const member of result.members) {
    const id = refID(member?.interface_ref, 'interface');
    if (!id || seen.has(id) || !member?.deltas || typeof member.deltas !== 'object')
      return false;
    seen.add(id);
  }
  return true;
}

export function terminalDiagnosticJob(state: unknown): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled';
}
