import type { RequestReceipt, StreamEnvelope } from './public-v1.generated';

export type RecoveryIdentity = Readonly<{ request_id: string; request_domain: 'workspace' | 'management'; request_epoch: string }>;
// Network failure never creates another key or repeats POST. Persist this
// identity in the UI workspace before dispatch and resolve it with GET.
export function receiptURL(identity: RecoveryIdentity): string {
 const query = new URLSearchParams({ domain: identity.request_domain, epoch: identity.request_epoch });
 return `/api/v1/requests/${encodeURIComponent(identity.request_id)}?${query}`;
}
export function receiptMatches(receipt: RequestReceipt, identity: RecoveryIdentity): boolean {
 return receipt.request_id === identity.request_id && receipt.request_domain === identity.request_domain && receipt.request_epoch === identity.request_epoch;
}
export class StreamTracker {
 private stream = '';
 private sequence = BigInt(0);
 accept(envelope: StreamEnvelope): 'hint' | 'ignore' | 'resync' {
  if (!/^[0-9]{1,20}$/.test(envelope.sequence)) return 'resync';
  const sequence = BigInt(envelope.sequence);
  if (sequence < BigInt(1) || sequence > BigInt('18446744073709551615')) return 'resync';
  if (this.stream !== envelope.stream_id) { this.stream = envelope.stream_id; this.sequence = sequence; return 'resync'; }
  if (sequence <= this.sequence) return 'ignore';
  const gap = sequence !== this.sequence + BigInt(1);
  this.sequence = sequence;
  if (gap || envelope.type === 'resync.required') return 'resync';
  if (envelope.type === 'resource.changed' || envelope.type === 'state.coalesced') return 'hint';
  return 'ignore';
 }
}
export function knownAction(state: string, knownStates: ReadonlySet<string>, allowed: readonly string[], action: string, understoodActions: ReadonlySet<string>): boolean {
 return knownStates.has(state) && understoodActions.has(action) && allowed.includes(action);
}

// Bind outstanding REST responses to the current resource and permission
// context. A later refresh invalidates older in-flight responses; a context
// reset also invalidates responses from a previous account or policy revision.
export class ResponseFence {
 private context = '';
 private revision = 0;
 begin(context: string): Readonly<{ context: string; revision: number }> {
  this.context = context; this.revision++;
  return { context, revision: this.revision };
 }
 reset(): void { this.context = ''; this.revision++; }
 accepts(ticket: Readonly<{ context: string; revision: number }>): boolean {
  return ticket.context === this.context && ticket.revision === this.revision;
 }
}
