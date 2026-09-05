import type {
  Problem,
  RequestRecord,
  StartSafeApplyRequest,
  TransactionResource,
} from './types.generated';

// The opt-in local lab uses this gateway and recovery protocol. The default
// prototype retains its explicit reducer. CoreHttpClient validates the node and
// session boundary and never automatically retries a write.
export interface ChangeControlGateway {
  startSafeApply(
    command: StartSafeApplyRequest,
  ): Promise<
    | { kind: 'accepted'; transaction: TransactionResource }
    | { kind: 'problem'; problem: Problem }
  >;
  readRequest(requestId: string): Promise<RequestRecord | null>;
  readTransaction(transactionId: string): Promise<TransactionResource>;
}

// Only this non-secret identifier is needed to recover a lost acceptance reply.
// The candidate and authoritative request ledger remain server-persistent.
export type SubmissionHandle = { requestId: string; nodeId: string };
export type SubmissionResult =
  | {
      kind: 'accepted';
      handle: SubmissionHandle;
      transaction: TransactionResource;
    }
  | { kind: 'rejected'; handle: SubmissionHandle; problem: Problem }
  | { kind: 'unknown'; handle: SubmissionHandle };

function isOriginalTransaction(
  transaction: TransactionResource,
  handle: SubmissionHandle,
): boolean {
  return (
    transaction.requestId === handle.requestId &&
    transaction.nodeId === handle.nodeId
  );
}

function rejectedWithEvidence(
  problem: Problem,
  handle: SubmissionHandle,
): boolean {
  return (
    problem.requestId === handle.requestId &&
    problem.commandEffect === 'not-started'
  );
}

export async function submitSafeApplyOnce(
  gateway: ChangeControlGateway,
  nodeId: string,
  command: StartSafeApplyRequest,
): Promise<SubmissionResult> {
  const handle = { requestId: command.requestId, nodeId };
  try {
    const reply = await gateway.startSafeApply(command);
    if (reply.kind === 'accepted') {
      const tx = reply.transaction;
      if (
        isOriginalTransaction(tx, handle) &&
        tx.candidateId === command.candidateId &&
        tx.candidateRevision === command.expectedCandidateRevision
      )
        return { kind: 'accepted', handle, transaction: tx };
    } else if (rejectedWithEvidence(reply.problem, handle)) {
      return { kind: 'rejected', handle, problem: reply.problem };
    }
  } catch {
    // A timeout, abort, lost socket, or invalid reply may follow server acceptance.
    // Never convert it into Not Applied or make a second submission here.
  }
  return { kind: 'unknown', handle };
}

export async function recoverSafeApply(
  gateway: ChangeControlGateway,
  handle: SubmissionHandle,
): Promise<SubmissionResult> {
  try {
    const record = await gateway.readRequest(handle.requestId);
    if (
      !record ||
      record.requestId !== handle.requestId ||
      record.nodeId !== handle.nodeId ||
      record.operation !== 'safe-apply'
    )
      return { kind: 'unknown', handle };
    if (
      record.state === 'rejected' &&
      record.problem &&
      rejectedWithEvidence(record.problem, handle)
    )
      return { kind: 'rejected', handle, problem: record.problem };
    if (record.state === 'accepted' && record.transactionId) {
      const transaction = await gateway.readTransaction(record.transactionId);
      if (
        transaction.id === record.transactionId &&
        isOriginalTransaction(transaction, handle) &&
        record.candidateRevision !== null &&
        transaction.candidateRevision === record.candidateRevision
      )
        return { kind: 'accepted', handle, transaction };
    }
  } catch {
    // Recovery is read-only and remains uncertain when evidence is unavailable.
  }
  return { kind: 'unknown', handle };
}

export type ObservationContext = {
  connected: boolean;
  fresh: boolean;
  receivedAtMonotonicMs: number;
  nowMonotonicMs: number;
};

export function estimatedRemainingSeconds(
  transaction: TransactionResource,
  context: ObservationContext,
): number | null {
  if (transaction.confirmationDeadline === null) return null;
  const remainingAtRead =
    Date.parse(transaction.confirmationDeadline) -
    Date.parse(transaction.serverTime);
  const elapsed = context.nowMonotonicMs - context.receivedAtMonotonicMs;
  if (
    !Number.isFinite(remainingAtRead) ||
    !Number.isFinite(elapsed) ||
    elapsed < 0
  )
    return null;
  return Math.max(0, Math.ceil((remainingAtRead - elapsed) / 1000));
}

export type TransactionPresentation = {
  state:
    | 'pending'
    | 'awaiting-confirmation'
    | 'checking-outcome'
    | 'confirmed'
    | 'rolled-back'
    | 'not-applied'
    | 'rollback-conflict'
    | 'degraded'
    | 'needs-attention';
  remainingSeconds: number | null;
  canConfirm: boolean;
  canRollback: boolean;
  canReconcile: boolean;
  blocksNewApply: boolean;
};

export function presentTransaction(
  transaction: TransactionResource,
  context: ObservationContext,
): TransactionPresentation {
  const remainingSeconds = estimatedRemainingSeconds(transaction, context);
  const authoritative = context.connected && context.fresh;
  const awaiting =
    transaction.phase === 'awaiting-confirmation' &&
    transaction.safeApply === 'awaiting-confirmation';
  const knownApplied =
    transaction.outcome === 'applied' &&
    transaction.evidence.databaseCommit === 'committed' &&
    transaction.evidence.daemonApply === 'applied';
  let state: TransactionPresentation['state'] = 'pending';
  if (!authoritative || transaction.knowledge === 'outcome-unknown')
    state = 'checking-outcome';
  else if (transaction.safeApply === 'rollback-conflict')
    state = 'rollback-conflict';
  else if (transaction.outcome === 'degraded') state = 'degraded';
  else if (transaction.outcome === 'needs-attention') state = 'needs-attention';
  else if (transaction.phase === 'settled') {
    if (transaction.safeApply === 'confirmed' && knownApplied)
      state = 'confirmed';
    else if (
      transaction.safeApply === 'rolled-back' &&
      transaction.outcome === 'not-applied'
    )
      state = 'rolled-back';
    else if (
      transaction.safeApply === 'not-started' &&
      transaction.outcome === 'not-applied'
    )
      state = 'not-applied';
    else state = 'checking-outcome';
  } else if (awaiting) {
    state =
      knownApplied && remainingSeconds !== null && remainingSeconds > 0
        ? 'awaiting-confirmation'
        : 'checking-outcome';
  }
  const canDecide =
    authoritative &&
    state === 'awaiting-confirmation' &&
    transaction.locksCandidate;
  const terminal = ['confirmed', 'rolled-back', 'not-applied'].includes(state);
  return {
    state,
    remainingSeconds,
    canConfirm:
      canDecide &&
      transaction.allowedActions.includes('confirm') &&
      transaction.evidence.health === 'passed' &&
      transaction.evidence.checkpoint === 'ready',
    canRollback:
      canDecide &&
      transaction.allowedActions.includes('rollback') &&
      transaction.evidence.checkpoint === 'ready',
    canReconcile:
      authoritative && transaction.allowedActions.includes('reconcile'),
    // A single terminal transaction is necessary but not sufficient: the caller
    // must also check workspace.nodeWriteBlocked and candidate/validation gates.
    blocksNewApply: transaction.locksCandidate || !terminal,
  };
}

export function acceptTransactionSnapshot(
  previous: TransactionResource,
  incoming: TransactionResource,
): TransactionResource {
  if (
    incoming.id !== previous.id ||
    incoming.nodeId !== previous.nodeId ||
    incoming.requestId !== previous.requestId ||
    incoming.candidateId !== previous.candidateId ||
    incoming.candidateRevision !== previous.candidateRevision ||
    incoming.sequence < previous.sequence
  )
    return previous;
  if (incoming.sequence === previous.sequence) {
    // Read-time metadata is not a state version. Refresh the clock anchor on an
    // unchanged heartbeat, without allowing same-version data to rewrite state.
    // The caller must keep receivedAtMonotonicMs paired with the accepted read;
    // it must not reset that timestamp when this function returns previous.
    const previousTime = Date.parse(previous.serverTime);
    const incomingTime = Date.parse(incoming.serverTime);
    return Number.isFinite(incomingTime) && incomingTime > previousTime
      ? { ...previous, serverTime: incoming.serverTime }
      : previous;
  }
  return incoming;
}
