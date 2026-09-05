import {
  ApiProblemError,
  HttpContractError,
  type CandidateSnapshot,
  type CoreHttpClient,
} from './http-client.ts';
import type {
  CandidateMutation,
  PortsPage,
  WorkspaceSnapshot,
  ValidationRequest,
  JobResource,
  TransactionResource,
  EvidencePage,
  Problem,
} from './types.generated';
import {
  acceptTransactionSnapshot,
  presentTransaction,
  submitSafeApplyOnce,
} from './change-control.ts';

export type TransactionRecoveryHint = {
  nodeId: string;
  requestId: string;
  operation: 'safe-apply' | 'decision' | 'reconciliation';
  transactionId?: string;
};
export type RecoveryHintStore = {
  read: () => TransactionRecoveryHint | null;
  write: (hint: TransactionRecoveryHint | null) => void;
};
export function parseTransactionRecoveryHint(
  raw: string | null,
  nodeId: string,
): TransactionRecoveryHint | null {
  if (!raw) return null;
  const hint = JSON.parse(raw);
  const validId = (value: unknown) =>
    typeof value === 'string' &&
    /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
  if (
    !hint ||
    hint.nodeId !== nodeId ||
    !validId(hint.requestId) ||
    !['safe-apply', 'decision', 'reconciliation'].includes(hint.operation) ||
    (hint.operation !== 'safe-apply' && !validId(hint.transactionId)) ||
    Object.keys(hint).some(
      (key) =>
        !['nodeId', 'requestId', 'operation', 'transactionId'].includes(key),
    )
  )
    throw new HttpContractError(
      'Saved request hint is invalid. Check the server workspace before starting another operation.',
    );
  return hint;
}

export type WorkspaceState = {
  phase:
    | 'idle'
    | 'loading'
    | 'ready'
    | 'writing'
    | 'unknown'
    | 'error'
    | 'signed-out';
  snapshot: CandidateSnapshot | null;
  workspace: WorkspaceSnapshot | null;
  inventory: PortsPage | null;
  pendingRequestId: string | null;
  pendingValidation: ValidationRequest | null;
  validationJob: JobResource | null;
  transaction: TransactionResource | null;
  transactionJob: JobResource | null;
  evidence: EvidencePage | null;
  pendingTransaction: TransactionRecoveryHint | null;
  receivedAt: number;
  transactionReceivedAt: number;
  message: string;
  permissionError: boolean;
};
const empty = (): WorkspaceState => ({
  phase: 'idle',
  snapshot: null,
  workspace: null,
  inventory: null,
  pendingRequestId: null,
  pendingValidation: null,
  validationJob: null,
  transaction: null,
  transactionJob: null,
  evidence: null,
  pendingTransaction: null,
  receivedAt: 0,
  transactionReceivedAt: 0,
  message: '',
  permissionError: false,
});

export async function readPinnedInventory(
  client: Pick<CoreHttpClient, 'listPorts'>,
): Promise<PortsPage> {
  const first = await client.listPorts();
  const items = [...first.items];
  const ids = new Set(items.map((item) => item.id));
  const cursors = new Set<string>();
  let cursor = first.nextCursor;
  if (ids.size !== items.length)
    throw new HttpContractError('Duplicate Port identities in inventory.');
  while (cursor) {
    if (cursors.has(cursor) || cursors.size >= 64)
      throw new HttpContractError(
        'Inventory pagination exceeded its bounded snapshot.',
      );
    cursors.add(cursor);
    const next = await client.listPorts({ cursor });
    if (
      next.nodeId !== first.nodeId ||
      next.snapshotId !== first.snapshotId ||
      next.generation !== first.generation ||
      next.availability !== first.availability
    )
      throw new HttpContractError(
        'Inventory changed between pages. Refresh the snapshot.',
      );
    for (const item of next.items) {
      if (ids.has(item.id))
        throw new HttpContractError('Duplicate Port across inventory pages.');
      ids.add(item.id);
      items.push(item);
    }
    first.warnings = [...new Set([...first.warnings, ...next.warnings])];
    cursor = next.nextCursor;
  }
  return { ...first, items, nextCursor: null };
}

// One instance is bound to one authenticated session epoch. Dispose before a user
// switch; late responses from this instance can never repopulate the next user's UI.
export class WorkspaceController {
  private state: WorkspaceState = empty();
  private version = 0;
  private disposed = false;
  private listeners = new Set<() => void>();
  private readonly client: CoreHttpClient;
  private readonly hints?: RecoveryHintStore;
  constructor(client: CoreHttpClient, hints?: RecoveryHintStore) {
    this.client = client;
    this.hints = hints;
    const pending = hints?.read();
    if (pending) {
      if (pending.nodeId !== client.nodeId)
        throw new HttpContractError('Request hint belongs to another node.');
      this.state = {
        ...empty(),
        phase: 'unknown',
        pendingRequestId: pending.requestId,
        pendingTransaction: pending,
        message:
          'Restoring the original transaction request before enabling new commands.',
      };
    }
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  private set(state: WorkspaceState) {
    if (this.disposed) return;
    this.state = state;
    this.listeners.forEach((listener) => listener());
  }
  private fail(
    error: unknown,
    pending: string | null = this.state.pendingRequestId,
  ) {
    const denied =
      error instanceof ApiProblemError &&
      [401, 403].includes(error.problem.status);
    if (denied)
      this.set({
        ...empty(),
        phase: error.problem.status === 401 ? 'signed-out' : 'error',
        permissionError: true,
        message: error.problem.detail,
      });
    else
      this.set({
        ...this.state,
        phase: pending ? 'unknown' : 'error',
        pendingRequestId: pending,
        message:
          error instanceof Error
            ? error.message
            : 'Unable to read server state.',
      });
  }
  dispose() {
    this.version++;
    this.set({ ...empty(), phase: 'signed-out' });
    this.disposed = true;
    this.listeners.clear();
  }
  canWrite() {
    const { phase, workspace, snapshot, pendingRequestId } = this.state;
    return (
      phase === 'ready' &&
      !pendingRequestId &&
      Boolean(
        workspace?.permissions.editCandidate &&
        snapshot &&
        !workspace.nodeWriteBlocked &&
        !snapshot.candidate.lockedByTransactionId &&
        !workspace.activeTransactions.some((item) => item.locksCandidate) &&
        !workspace.pendingRequests.some((item) => item.state === 'recorded'),
      )
    );
  }
  canValidate() {
    const { phase, workspace, snapshot, pendingRequestId } = this.state;
    return (
      phase === 'ready' &&
      !pendingRequestId &&
      Boolean(
        workspace?.permissions.validate &&
        snapshot?.candidate.intents.length &&
        snapshot.candidate.freshness === 'current' &&
        !workspace.nodeWriteBlocked &&
        !snapshot.candidate.lockedByTransactionId &&
        !workspace.activeTransactions.some((item) => item.locksCandidate) &&
        !workspace.pendingRequests.some((item) => item.state === 'recorded') &&
        !['pending', 'running'].includes(
          workspace.latestValidation?.status ?? '',
        ),
      )
    );
  }
  transactionPresentation(now = performance.now()) {
    return this.state.transaction
      ? presentTransaction(this.state.transaction, {
          connected: this.state.phase === 'ready',
          fresh: this.state.phase === 'ready',
          receivedAtMonotonicMs: this.state.transactionReceivedAt,
          nowMonotonicMs: now,
        })
      : null;
  }
  canStartSafeApply() {
    const { workspace, snapshot, transaction } = this.state;
    const validation = workspace?.latestValidation;
    const serverNow =
      Date.parse(workspace?.serverTime ?? '') +
      performance.now() -
      this.state.receivedAt;
    return (
      this.canWrite() &&
      Boolean(
        workspace?.permissions.startSafeApply &&
        !transaction?.locksCandidate &&
        validation?.status === 'passed' &&
        validation.expiresAt &&
        Date.parse(validation.expiresAt) > serverNow &&
        validation.candidateRevision === snapshot?.candidate.revision &&
        validation.generation === snapshot?.candidate.currentGeneration &&
        snapshot?.candidate.freshness === 'current',
      )
    );
  }
  private async load() {
    const workspace = await this.client.readWorkspace();
    const receivedAt = performance.now();
    const [snapshot, inventory] = await Promise.all([
      this.client.readCandidate(),
      readPinnedInventory(this.client).catch((error: unknown) => {
        if (
          error instanceof ApiProblemError &&
          error.problem.code === 'PROVIDER_UNAVAILABLE'
        )
          return null;
        throw error;
      }),
    ]);
    if (
      workspace.candidate.id !== snapshot.candidate.id ||
      workspace.candidate.revision !== snapshot.candidate.revision ||
      workspace.candidate.currentGeneration !==
        snapshot.candidate.currentGeneration ||
      workspace.candidate.lockedByTransactionId !==
        snapshot.candidate.lockedByTransactionId ||
      (inventory &&
        inventory.generation !== snapshot.candidate.currentGeneration)
    )
      throw new HttpContractError(
        'Workspace changed while loading. Refresh and review again.',
      );
    const validationJob = workspace.latestValidation
      ? await this.client.readValidationJob(workspace.latestValidation)
      : null;
    let incoming =
      workspace.latestTransaction ?? workspace.activeTransactions[0] ?? null;
    let transactionReceivedAt = receivedAt;
    if (!incoming && this.state.transaction) {
      incoming = await this.client.readTransaction(this.state.transaction.id);
      transactionReceivedAt = performance.now();
    }
    const previous = this.state.transaction;
    const transaction =
      previous && incoming?.id === previous.id
        ? acceptTransactionSnapshot(previous, incoming)
        : incoming;
    if (transaction === previous)
      transactionReceivedAt = this.state.transactionReceivedAt;
    const [transactionJob, evidence] = transaction
      ? await Promise.all([
          this.client.readTransactionJob(transaction),
          this.client.readEvidence(transaction.id),
        ])
      : [null, null];
    return {
      workspace,
      snapshot,
      inventory,
      validationJob,
      transaction,
      transactionJob,
      evidence,
      receivedAt,
      transactionReceivedAt,
    };
  }
  async refresh() {
    if (this.disposed || this.state.phase === 'writing') return;
    if (this.state.pendingRequestId) return this.recover();
    const version = ++this.version;
    this.set({
      ...this.state,
      phase: 'loading',
      message: '',
      permissionError: false,
    });
    try {
      const loaded = await this.load();
      if (version === this.version)
        this.set({ ...empty(), ...loaded, phase: 'ready' });
    } catch (error) {
      if (version === this.version) this.fail(error);
    }
  }
  async mutate(command: CandidateMutation) {
    if (!this.canWrite() || !this.state.snapshot) return false;
    command = structuredClone(command);
    const version = ++this.version;
    const reviewed = this.state.snapshot;
    this.set({
      ...this.state,
      phase: 'writing',
      pendingRequestId: command.requestId,
      message: 'Saving Candidate…',
    });
    try {
      const reply = await this.client.mutateCandidate(command, reviewed);
      if (version !== this.version || this.disposed) return false;
      if (reply.kind === 'unknown') {
        this.set({
          ...this.state,
          phase: 'unknown',
          message:
            'The save result is unknown. Check the original request before editing again.',
        });
        return false;
      }
      if (reply.kind === 'rejected') {
        if ([401, 403].includes(reply.problem.status)) {
          this.fail(new ApiProblemError(reply.problem), null);
          return false;
        }
        this.set({
          ...this.state,
          phase: 'error',
          pendingRequestId: null,
          message: reply.problem.detail,
        });
        return false;
      }
      // A success acknowledges this save. Read a coherent workspace before more writes.
      const loaded = await this.load();
      if (version !== this.version || this.disposed) return false;
      this.set({
        ...empty(),
        ...loaded,
        phase: 'ready',
        message:
          'Candidate saved on the local server. Running configuration is unchanged.',
      });
      return true;
    } catch (error) {
      if (version === this.version) this.fail(error, command.requestId);
      return false;
    }
  }
  async validate(requestId: string) {
    if (!this.canValidate() || !this.state.snapshot) return false;
    const candidate = this.state.snapshot.candidate;
    const command: ValidationRequest = {
      requestId,
      candidateId: candidate.id,
      expectedCandidateRevision: candidate.revision,
      expectedGeneration: candidate.currentGeneration,
    };
    const version = ++this.version;
    this.set({
      ...this.state,
      phase: 'writing',
      pendingRequestId: requestId,
      pendingValidation: command,
      message: 'Requesting server validation…',
    });
    try {
      const reply = await this.client.validateCandidate(command);
      if (version !== this.version || this.disposed) return false;
      if (reply.kind === 'unknown') {
        this.set({
          ...this.state,
          phase: 'unknown',
          message:
            'The validation response was lost. Check the original request before submitting another validation.',
        });
        return false;
      }
      if (reply.kind === 'rejected') {
        if ([401, 403].includes(reply.problem.status))
          this.fail(new ApiProblemError(reply.problem), null);
        else
          this.set({
            ...this.state,
            phase: 'error',
            pendingRequestId: null,
            pendingValidation: null,
            message: reply.problem.detail,
          });
        return false;
      }
      const loaded = await this.load();
      if (version !== this.version || this.disposed) return false;
      this.set({
        ...empty(),
        ...loaded,
        phase: 'ready',
        message:
          'Validation accepted. Its server job continues if this page closes.',
      });
      return true;
    } catch (error) {
      if (version === this.version) this.fail(error, requestId);
      return false;
    }
  }
  async pollValidation() {
    if (
      this.disposed ||
      this.state.phase !== 'ready' ||
      !this.state.workspace?.latestValidation
    )
      return;
    const version = ++this.version;
    try {
      const loaded = await this.load();
      if (version === this.version && !this.disposed)
        this.set({ ...this.state, ...loaded });
    } catch (error) {
      if (version === this.version) this.fail(error, null);
    }
  }
  async recover() {
    if (this.state.pendingTransaction) return this.recoverTransaction();
    const requestId = this.state.pendingRequestId;
    const validation = this.state.pendingValidation;
    if (!requestId || this.disposed || this.state.phase === 'writing') return;
    const version = ++this.version;
    this.set({
      ...this.state,
      phase: 'loading',
      message: 'Reading original request evidence…',
    });
    try {
      const record = await this.client.readRequest(requestId);
      if (version !== this.version || this.disposed) return;
      if (
        !record ||
        record.operation !== (validation ? 'validation' : 'candidate') ||
        record.nodeId !== this.client.nodeId ||
        record.requestId !== requestId ||
        record.state === 'recorded' ||
        (record.state === 'rejected' &&
          (!record.problem ||
            record.problem.commandEffect !== 'not-started' ||
            record.problem.requestId !== requestId))
      ) {
        this.set({
          ...this.state,
          phase: 'unknown',
          message:
            'The request ledger has no conclusive result yet. No save was retried.',
        });
        return;
      }
      if (validation && record.state === 'accepted') {
        if (
          !record.validationId ||
          !record.jobId ||
          record.candidateRevision !== validation.expectedCandidateRevision
        )
          throw new HttpContractError(
            'Original validation evidence is incomplete.',
          );
        const restored = await this.client.readValidation(record.validationId);
        if (
          restored.requestId !== requestId ||
          restored.jobId !== record.jobId ||
          restored.candidateId !== validation.candidateId ||
          restored.candidateRevision !== validation.expectedCandidateRevision ||
          restored.generation !== validation.expectedGeneration
        )
          throw new HttpContractError(
            'Original validation evidence does not match the submitted snapshot.',
          );
      }
      const loaded = await this.load();
      if (version === this.version)
        this.set({
          ...empty(),
          ...loaded,
          phase: 'ready',
          message: validation
            ? 'Original validation request checked. Latest server diff and result restored.'
            : record.state === 'accepted'
              ? 'The original save was accepted. Latest Candidate restored.'
              : 'The original save was rejected. Latest Candidate restored for review.',
        });
    } catch (error) {
      if (version === this.version) this.fail(error, requestId);
    }
  }

  private async transactionWrite(
    hint: TransactionRecoveryHint,
    send: () => Promise<
      | { kind: 'accepted' }
      | { kind: 'rejected'; problem: Problem }
      | { kind: 'unknown' }
    >,
  ) {
    const version = ++this.version;
    try {
      this.hints?.write(hint);
    } catch {
      this.set({
        ...this.state,
        phase: 'error',
        message: 'Unable to save the recovery identifier. No command was sent.',
      });
      return false;
    }
    this.set({
      ...this.state,
      phase: 'writing',
      pendingRequestId: hint.requestId,
      pendingTransaction: hint,
      message: 'Submitting one protected command…',
    });
    try {
      const result = await send();
      if (this.disposed || version !== this.version) return false;
      if (result.kind === 'unknown') {
        this.set({
          ...this.state,
          phase: 'unknown',
          message:
            'OutcomeUnknown: read the original request before sending any new command.',
        });
        return false;
      }
      if (result.kind === 'rejected') {
        this.hints?.write(null);
        this.set({
          ...this.state,
          phase: 'error',
          pendingRequestId: null,
          pendingTransaction: null,
          message: result.problem.detail,
        });
        if ([401, 403].includes(result.problem.status))
          this.fail(new ApiProblemError(result.problem), null);
        return false;
      }
      const loaded = await this.load();
      if (this.disposed || version !== this.version) return false;
      this.hints?.write(null);
      this.set({
        ...empty(),
        ...loaded,
        phase: 'ready',
        message:
          'Command accepted. The server owns the transaction and recovery window.',
      });
      return true;
    } catch (error) {
      if (version === this.version) this.fail(error, hint.requestId);
      return false;
    }
  }
  async startSafeApply(reason: string, requestId: string) {
    if (!this.canStartSafeApply() || !reason.trim() || reason.length > 2000)
      return false;
    const candidate = this.state.snapshot!.candidate;
    const command = {
      requestId,
      candidateId: candidate.id,
      expectedCandidateRevision: candidate.revision,
      expectedGeneration: candidate.currentGeneration,
      validationId: this.state.workspace!.latestValidation!.id,
      reason,
    };
    return this.transactionWrite(
      { operation: 'safe-apply', nodeId: this.client.nodeId, requestId },
      () => submitSafeApplyOnce(this.client, this.client.nodeId, command),
    );
  }
  async decideSafeApply(
    decision: 'confirm' | 'rollback',
    reason: string,
    requestId: string,
  ) {
    const presentation = this.transactionPresentation();
    const tx = this.state.transaction;
    if (
      !tx ||
      this.state.pendingRequestId ||
      !this.state.workspace?.permissions.editCandidate ||
      !(decision === 'confirm'
        ? presentation?.canConfirm
        : presentation?.canRollback) ||
      !reason.trim() ||
      reason.length > 2000
    )
      return false;
    return this.transactionWrite(
      {
        operation: 'decision',
        nodeId: this.client.nodeId,
        requestId,
        transactionId: tx.id,
      },
      () =>
        this.client.decideSafeApply(tx.id, {
          requestId,
          decision,
          reason,
          expectedTransactionSequence: tx.sequence,
        }),
    );
  }
  async reconcileTransaction(requestId: string) {
    const tx = this.state.transaction;
    if (
      !tx ||
      this.state.pendingRequestId ||
      !this.transactionPresentation()?.canReconcile ||
      !this.state.workspace?.permissions.editCandidate
    )
      return false;
    return this.transactionWrite(
      {
        operation: 'reconciliation',
        nodeId: this.client.nodeId,
        requestId,
        transactionId: tx.id,
      },
      () => this.client.reconcileTransaction(tx.id, { requestId }),
    );
  }
  private async recoverTransaction() {
    const hint = this.state.pendingTransaction;
    if (!hint || this.disposed || this.state.phase === 'writing') return;
    const version = ++this.version;
    this.set({
      ...this.state,
      phase: 'loading',
      message: 'Reading the original transaction request…',
    });
    try {
      const record = await this.client.readRequest(hint.requestId);
      if (
        !record ||
        record.nodeId !== hint.nodeId ||
        record.requestId !== hint.requestId ||
        record.operation !== hint.operation ||
        record.state === 'recorded' ||
        (record.state === 'rejected' &&
          (!record.problem ||
            record.problem.requestId !== hint.requestId ||
            record.problem.commandEffect !== 'not-started'))
      )
        throw new HttpContractError(
          'The original request still has no conclusive result. No command was retried.',
        );
      if (record.state === 'accepted') {
        if (
          !record.transactionId ||
          (hint.transactionId && hint.transactionId !== record.transactionId)
        )
          throw new HttpContractError(
            'Original transaction identity does not match.',
          );
        const tx = await this.client.readTransaction(record.transactionId);
        if (
          tx.candidateRevision !== record.candidateRevision ||
          (hint.operation === 'safe-apply' && tx.requestId !== hint.requestId)
        )
          throw new HttpContractError(
            'The request ledger does not match the original transaction.',
          );
        if (hint.operation === 'reconciliation') {
          if (!record.jobId)
            throw new HttpContractError('Missing reconciliation job.');
          const job = await this.client.readJob(record.jobId);
          if (
            job.kind !== 'reconciliation' ||
            job.transactionId !== tx.id ||
            job.correlationId !== hint.requestId
          )
            throw new HttpContractError(
              'Reconciliation evidence does not match.',
            );
        }
      }
      const loaded = await this.load();
      if (this.disposed || version !== this.version) return;
      this.hints?.write(null);
      this.set({
        ...empty(),
        ...loaded,
        phase: 'ready',
        message:
          'Original request checked. Authoritative transaction and evidence restored.',
      });
    } catch (error) {
      if (version === this.version) this.fail(error, hint.requestId);
    }
  }
  async loadMoreEvidence() {
    const tx = this.state.transaction;
    const evidence = this.state.evidence;
    if (
      this.disposed ||
      this.state.phase !== 'ready' ||
      !tx ||
      !evidence?.nextCursor
    )
      return;
    const version = this.version;
    try {
      const next = await this.client.readEvidence(tx.id, evidence.nextCursor);
      if (version === this.version && !this.disposed)
        this.set({
          ...this.state,
          evidence: { ...next, items: [...evidence.items, ...next.items] },
        });
    } catch (error) {
      if (version === this.version) this.fail(error, null);
    }
  }
}
