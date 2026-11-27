const partBytes = 24 * 1024;

type Chunk = { id: string; part: number; data_base64: string };
type Artifact = { id: string; bytes: number; parts: number; sha256: string };
type Bounds = {
  min: number;
  max: number;
  expires: number;
  errorPrefix: 'SUPPORT_BUNDLE' | 'CAPTURE_ARTIFACT';
};

export async function readVerifiedArtifact(
  id: string,
  metadata: Artifact,
  bounds: Bounds,
  readPart: (index: number) => Promise<Chunk>,
  active: () => boolean,
  now = Date.now,
): Promise<Uint8Array<ArrayBuffer>> {
  const fail = (reason: string) => new Error(`${bounds.errorPrefix}_${reason}`);
  const assertCurrent = () => {
    if (!active()) throw fail('CONTEXT_CHANGED');
    if (!Number.isFinite(bounds.expires) || now() >= bounds.expires)
      throw fail('EXPIRED');
  };
  assertCurrent();
  if (
    metadata.id !== id ||
    !/^[a-f0-9]{64}$/.test(metadata.sha256) ||
    !Number.isSafeInteger(metadata.bytes) ||
    metadata.bytes < bounds.min ||
    metadata.bytes > bounds.max ||
    metadata.parts !== Math.ceil(metadata.bytes / partBytes)
  )
    throw fail('METADATA_INVALID');

  const artifact = new Uint8Array(metadata.bytes);
  for (let index = 0; index < metadata.parts; index++) {
    assertCurrent();
    const part = await readPart(index);
    assertCurrent();
    const offset = index * partBytes;
    const count = Math.min(partBytes, metadata.bytes - offset);
    if (
      part?.id !== id ||
      part.part !== index ||
      typeof part.data_base64 !== 'string' ||
      part.data_base64.length !== 4 * Math.ceil(count / 3) ||
      !/^[A-Za-z0-9+/]+={0,2}$/.test(part.data_base64)
    )
      throw fail('PART_INVALID');
    const decoded = atob(part.data_base64);
    if (decoded.length !== count) throw fail('PART_INVALID');
    for (let j = 0; j < count; j++)
      artifact[offset + j] = decoded.charCodeAt(j);
  }

  const digest = new Uint8Array(
    await crypto.subtle.digest('SHA-256', artifact),
  );
  assertCurrent();
  const hex = Array.from(digest, (value) =>
    value.toString(16).padStart(2, '0'),
  ).join('');
  if (hex !== metadata.sha256) throw fail('CHECKSUM_MISMATCH');
  return artifact;
}

export function saveVerifiedArtifact(
  bytes: Uint8Array<ArrayBuffer>,
  filename: string,
  type: string,
) {
  const url = URL.createObjectURL(new Blob([bytes], { type }));
  const link = document.createElement('a');
  try {
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
  } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
}
