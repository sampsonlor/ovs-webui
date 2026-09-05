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
} from './types.generated';

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
  message: string;
  permissionError: boolean;
};
const empty = (): WorkspaceState => ({
  phase: 'idle',
  snapshot: null,
  workspace: null,
  inventory: null,
  pendingRequestId: null,
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
  constructor(client: CoreHttpClient) {
    this.client = client;
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
  private async load() {
    const workspace = await this.client.readWorkspace();
    const [snapshot, inventory] = await Promise.all([
      this.client.readCandidate(),
      readPinnedInventory(this.client),
    ]);
    if (
      workspace.candidate.id !== snapshot.candidate.id ||
      workspace.candidate.revision !== snapshot.candidate.revision ||
      workspace.candidate.currentGeneration !==
        snapshot.candidate.currentGeneration ||
      inventory.generation !== snapshot.candidate.currentGeneration
    )
      throw new HttpContractError(
        'Workspace changed while loading. Refresh and review again.',
      );
    return { workspace, snapshot, inventory };
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
  async recover() {
    const requestId = this.state.pendingRequestId;
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
        record.operation !== 'candidate' ||
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
      const loaded = await this.load();
      if (version === this.version)
        this.set({
          ...empty(),
          ...loaded,
          phase: 'ready',
          message:
            record.state === 'accepted'
              ? 'The original save was accepted. Latest Candidate restored.'
              : 'The original save was rejected. Latest Candidate restored for review.',
        });
    } catch (error) {
      if (version === this.version) this.fail(error, requestId);
    }
  }
}
