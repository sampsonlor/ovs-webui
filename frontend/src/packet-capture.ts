import type {
  CaptureArtifact,
  CapturePart,
  Session,
} from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';
import { readVerifiedArtifact } from './artifact-download.ts';

export const captureRoute = '/operations/packet-capture';
export const capturePath = '/captures';
export const captureOperation = 'createCapture';
export const captureMaxBytes = 1024 * 1024;
export const captureMinBytes = 54;
export const captureMaxPackets = 4096;
const managementID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const cancellation = /^\/jobs\/([0-9a-f-]{36})\/cancellations$/;

export type CaptureProfile = CaptureArtifact['profile'];
export const captureProfiles = [
  { id: 'arp-ethernet-headers', label: 'ARP Ethernet headers' },
  {
    id: 'slow-protocol-ethernet-headers',
    label: 'Slow Protocol (including LACP) Ethernet headers',
  },
  { id: 'lldp-ethernet-headers', label: 'LLDP Ethernet headers' },
] as const;

export function capturePermission(session: Session | null): boolean {
  return has(session, 'diagnostics.disruptive');
}

export function captureElevationExpiry(session: Session | null): number {
  const value = session?.elevated_until;
  return typeof value === 'string' ? Date.parse(value) : 0;
}

export function captureResultID(query: string): string | null {
  const id = new URLSearchParams(query).get('job_id');
  return id && managementID.test(id) ? id : null;
}

export function captureResultRoute(
  id: string,
  session: Session | null,
): string | null {
  if (!managementID.test(id)) return null;
  if (has(session, 'jobs.read')) return `/operations/jobs/${id}`;
  return `${captureRoute}?job_id=${id}`;
}

export function captureCancellationID(path: string): string | null {
  const id = cancellation.exec(path)?.[1];
  return id && managementID.test(id) ? id : null;
}

export function isCaptureJob(
  job: Record<string, unknown> | null,
  session: Session | null,
): boolean {
  return (
    !!session &&
    job?.operation === captureOperation &&
    job.owner_id === session.principal_id &&
    typeof job.id === 'string' &&
    managementID.test(job.id)
  );
}

export function canCancelCapture(
  job: Record<string, unknown>,
  session: Session | null,
): boolean {
  return (
    isCaptureJob(job, session) &&
    has(session, 'jobs.cancel') &&
    job.cancellable === true &&
    (job.state === 'queued' || job.state === 'running') &&
    Array.isArray(job.allowed_actions) &&
    job.allowed_actions.includes('cancel')
  );
}

export function captureInputsValid(
  interfaceID: string,
  duration: number,
  bytes: number,
  profile: CaptureProfile,
): boolean {
  return (
    managementID.test(interfaceID) &&
    Number.isInteger(duration) &&
    duration >= 1 &&
    duration <= 300 &&
    Number.isInteger(bytes) &&
    bytes >= captureMinBytes &&
    bytes <= captureMaxBytes &&
    captureProfiles.some(({ id }) => id === profile)
  );
}

export async function readVerifiedCapture(
  id: string,
  meta: CaptureArtifact,
  readPart: (index: number) => Promise<CapturePart>,
  active: () => boolean,
  now = Date.now,
) {
  if (
    !managementID.test(id) ||
    meta.target_ref?.kind !== 'interface' ||
    !managementID.test(meta.target_ref.id) ||
    !captureProfiles.some((profile) => profile.id === meta.profile) ||
    !Number.isSafeInteger(meta.packets) ||
    meta.packets < 0 ||
    meta.packets > captureMaxPackets ||
    meta.bytes !== 24 + 30 * meta.packets
  )
    throw new Error('CAPTURE_ARTIFACT_METADATA_INVALID');
  return readVerifiedArtifact(
    id,
    meta,
    {
      min: 24,
      max: captureMaxBytes,
      expires: Date.parse(meta.expires_at),
      errorPrefix: 'CAPTURE_ARTIFACT',
    },
    readPart,
    active,
    now,
  );
}
