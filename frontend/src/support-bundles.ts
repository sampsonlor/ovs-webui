import type {
  Session,
  SupportBundleMetadata,
  SupportBundlePart,
  SupportCommand,
} from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';
import { readVerifiedArtifact } from './artifact-download.ts';

export const supportBundleRoute = '/operations/support-bundles';
export const supportBundlePath = '/support-bundles';
export const supportBundleOperation = 'createSupportBundle';
const managementID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export type SupportKind = SupportCommand['include_kinds'][number];

export const supportScopes = [
  {
    kind: 'runtime',
    label: 'Runtime',
    detail: 'Application version and readiness',
    permission: 'state.read',
  },
  {
    kind: 'ovs',
    label: 'OVS',
    detail: 'Provider state and topology counts',
    permission: 'inventory.read',
  },
  {
    kind: 'health',
    label: 'System Health',
    detail: 'Management component observations',
    permission: 'state.read',
  },
  {
    kind: 'events',
    label: 'Events',
    detail: 'Recent operation outcomes and timestamps',
    permission: 'events.read',
  },
  {
    kind: 'audit',
    label: 'Audit',
    detail: 'Recent audit outcomes and timestamps',
    permission: 'audit.read',
  },
] as const;

export function supportBundleID(query: string): string | null {
  const id = new URLSearchParams(query).get('job_id');
  return id && managementID.test(id) ? id : null;
}

export function supportBundleResultRoute(id: string): string | null {
  return managementID.test(id) ? `${supportBundleRoute}?job_id=${id}` : null;
}

export function authorizedSupportScopes(
  kinds: readonly SupportKind[],
  session: Session | null,
): boolean {
  return (
    kinds.length > 0 &&
    kinds.length <= supportScopes.length &&
    new Set(kinds).size === kinds.length &&
    kinds.every((kind) =>
      supportScopes.some(
        (scope) => scope.kind === kind && has(session, scope.permission),
      ),
    )
  );
}

export function supportBundleExpiry(meta: SupportBundleMetadata): number {
  return Date.parse(meta.manifest.expires_at);
}

export async function readVerifiedBundle(
  id: string,
  meta: SupportBundleMetadata,
  readPart: (index: number) => Promise<SupportBundlePart>,
  active: () => boolean,
  now = Date.now,
): Promise<Uint8Array<ArrayBuffer>> {
  if (
    !managementID.test(id) ||
    meta.manifest.version !== 1 ||
    !Array.isArray(meta.manifest.sections) ||
    meta.manifest.sections.length < 1 ||
    meta.manifest.sections.length > supportScopes.length
  )
    throw new Error('SUPPORT_BUNDLE_METADATA_INVALID');
  return readVerifiedArtifact(
    id,
    meta,
    {
      min: 1,
      max: 512 * 1024,
      expires: supportBundleExpiry(meta),
      errorPrefix: 'SUPPORT_BUNDLE',
    },
    readPart,
    active,
    now,
  );
}
