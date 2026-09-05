import type { ChangeControlGateway } from './change-control';
import type {
  CandidateMutation,
  CandidateResource,
  PortResource,
  PortsPage,
  Problem,
  RequestRecord,
  StartSafeApplyRequest,
  TransactionResource,
  WorkspaceSnapshot,
  ValidationRequest,
  ValidationResource,
  JobResource,
  DecisionRequest,
  ReconciliationRequest,
  EvidencePage,
} from './types.generated';

export type HttpSchemas = {
  Id: string;
  PortsPage: PortsPage;
  PortResource: PortResource;
  CandidateResource: CandidateResource;
  CandidateMutation: CandidateMutation;
  WorkspaceSnapshot: WorkspaceSnapshot;
  ValidationRequest: ValidationRequest;
  ValidationResource: ValidationResource;
  JobResource: JobResource;
  StartSafeApplyRequest: StartSafeApplyRequest;
  TransactionResource: TransactionResource;
  RequestRecord: RequestRecord;
  Problem: Problem;
  DecisionRequest: DecisionRequest;
  ReconciliationRequest: ReconciliationRequest;
  EvidencePage: EvidencePage;
};

// Required: the caller must bind these names to the generated contract schemas.
// No unchecked JSON-to-TypeScript cast is exposed as a live integration default.
export type ValidateContract = (
  schema: keyof HttpSchemas,
  value: unknown,
) => boolean;
export type CandidateSnapshot = { candidate: CandidateResource; etag: string };
export type CandidateWriteResult =
  | { kind: 'accepted'; snapshot: CandidateSnapshot }
  | { kind: 'rejected'; problem: Problem }
  | { kind: 'unknown'; requestId: string; nodeId: string };
export type ValidationWriteResult =
  | { kind: 'accepted'; validation: ValidationResource }
  | { kind: 'rejected'; problem: Problem }
  | { kind: 'unknown'; requestId: string; nodeId: string };

export type TransactionWriteResult<T> =
  | { kind: 'accepted'; resource: T }
  | { kind: 'rejected'; problem: Problem }
  | { kind: 'unknown' };

export class HttpContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'HttpContractError';
  }
}
export class ApiProblemError extends Error {
  readonly problem: Problem;
  constructor(problem: Problem) {
    super(problem.title);
    this.name = 'ApiProblemError';
    this.problem = problem;
  }
}

function strongEtag(value: string | null): string {
  if (!value || !/^"[\x21\x23-\x7e\x80-\xff]+"$/.test(value))
    throw new HttpContractError(
      'Candidate requires a strong, quoted ETag from /candidate.',
    );
  return value;
}

/** Same-origin, one-node transport. Identity comes only from the server session. */
export class CoreHttpClient implements ChangeControlGateway {
  readonly nodeId: string;
  private readonly origin: string;
  private readonly validate: ValidateContract;
  private readonly transport: typeof fetch;
  private readonly csrfToken: () => string | Promise<string>;
  private readonly timeoutMs: number;

  constructor(options: {
    origin: string;
    nodeId: string;
    validate: ValidateContract;
    csrfToken: () => string | Promise<string>;
    fetch?: typeof fetch;
    timeoutMs?: number;
  }) {
    const url = new URL(options.origin);
    if (
      !['http:', 'https:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.pathname !== '/' ||
      url.search ||
      url.hash
    )
      throw new HttpContractError(
        'Configure the application origin, without credentials, a path, query or fragment.',
      );
    this.origin = url.origin;
    this.nodeId = options.nodeId;
    this.validate = options.validate;
    this.transport = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.csrfToken = options.csrfToken;
    this.timeoutMs = options.timeoutMs ?? 15_000;
    if (
      !Number.isSafeInteger(this.timeoutMs) ||
      this.timeoutMs < 1 ||
      this.timeoutMs > 120_000
    )
      throw new HttpContractError('Invalid request timeout.');
    this.check('Id', this.nodeId);
  }

  private check<K extends keyof HttpSchemas>(
    schema: K,
    value: unknown,
  ): HttpSchemas[K] {
    if (!this.validate(schema, value))
      throw new HttpContractError(`Invalid ${schema} payload.`);
    return value as HttpSchemas[K];
  }

  private sameNode<T extends { nodeId: string }>(resource: T): T {
    if (resource.nodeId !== this.nodeId)
      throw new HttpContractError('Response belongs to another node.');
    return resource;
  }

  private id(value: string): string {
    return encodeURIComponent(this.check('Id', value));
  }
  private validationResource(resource: ValidationResource): ValidationResource {
    this.sameNode(resource);
    if (
      resource.status === 'passed' &&
      (!resource.expiresAt ||
        !resource.diff.length ||
        !resource.checks.length ||
        !resource.safetyPlan ||
        resource.checks.some((check) =>
          ['block', 'unknown'].includes(check.state),
        ) ||
        [
          resource.safetyPlan.checkpoint,
          resource.safetyPlan.connectivityProbe,
          resource.safetyPlan.compareBeforeRollback,
        ].some((availability) => availability !== 'available'))
    )
      throw new HttpContractError(
        'Passed validation has incomplete or blocking evidence.',
      );
    return resource;
  }

  private async request<K extends keyof HttpSchemas>(
    path: string,
    schema: K,
    options: {
      status?: number;
      method?: 'GET' | 'POST' | 'PATCH';
      command?: { requestId: string };
      etag?: string;
    } = {},
  ): Promise<{ data: HttpSchemas[K]; response: Response }> {
    const method = options.method ?? 'GET';
    const headers = new Headers({
      Accept: 'application/json, application/problem+json',
    });
    if (options.command) {
      const token = await this.csrfToken();
      if (typeof token !== 'string' || !token.trim() || /[\r\n]/.test(token))
        throw new HttpContractError('A current CSRF token is required.');
      headers.set('Content-Type', 'application/json');
      headers.set('X-CSRF-Token', token);
      headers.set('Idempotency-Key', options.command.requestId);
    }
    if (options.etag) headers.set('If-Match', strongEtag(options.etag));
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const response = await this.transport(`${this.origin}/api/v1${path}`, {
        method,
        headers,
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        signal: controller.signal,
        ...(options.command ? { body: JSON.stringify(options.command) } : {}),
      });
      if (
        response.redirected ||
        (response.url && new URL(response.url).origin !== this.origin)
      )
        throw new HttpContractError('API redirects are not accepted.');
      const mediaType = response.headers
        .get('Content-Type')
        ?.split(';')[0]
        .trim()
        .toLowerCase();
      if (!response.ok) {
        if (mediaType !== 'application/problem+json')
          throw new HttpContractError(
            `HTTP ${response.status} without an authoritative Problem.`,
          );
        const problem = this.check('Problem', await response.json());
        if (
          problem.status !== response.status ||
          (options.command && problem.requestId !== options.command.requestId)
        )
          throw new HttpContractError('Mismatched Problem response.');
        throw new ApiProblemError(problem);
      }
      if (
        response.status !== (options.status ?? 200) ||
        mediaType !== 'application/json'
      )
        throw new HttpContractError('Unexpected API status or content type.');
      return { data: this.check(schema, await response.json()), response };
    } finally {
      clearTimeout(timer);
    }
  }

  async readWorkspace(): Promise<WorkspaceSnapshot> {
    const { data } = await this.request('/workspace', 'WorkspaceSnapshot');
    this.sameNode(data);
    this.sameNode(data.candidate);
    if (data.latestValidation) {
      this.validationResource(data.latestValidation);
      if (
        data.latestValidation.candidateId !== data.candidate.id ||
        (data.latestValidation.status !== 'expired' &&
          (data.latestValidation.candidateRevision !==
            data.candidate.revision ||
            data.latestValidation.generation !==
              data.candidate.currentGeneration))
      )
        throw new HttpContractError(
          'Validation does not match the workspace snapshot.',
        );
    }
    data.activeTransactions.forEach((item) => this.sameNode(item));
    if (data.latestTransaction) {
      this.sameNode(data.latestTransaction);
      if (data.latestTransaction.candidateId !== data.candidate.id)
        throw new HttpContractError(
          'Latest transaction belongs to another Candidate.',
        );
    }
    data.pendingRequests.forEach((item) => this.sameNode(item));
    return data;
  }

  async listPorts(
    options: { cursor?: string; search?: string } = {},
  ): Promise<PortsPage> {
    const query = new URLSearchParams();
    for (const name of ['cursor', 'search'] as const)
      if (options[name] !== undefined) {
        if (!options[name]!.trim())
          throw new HttpContractError(`${name} must not be empty.`);
        query.set(name, options[name]!);
      }
    const { data } = await this.request(
      `/ports${query.size ? `?${query}` : ''}`,
      'PortsPage',
    );
    this.sameNode(data);
    data.items.forEach((port) => {
      this.sameNode(port);
      if (port.generation !== data.generation)
        throw new HttpContractError('Mixed inventory generations.');
    });
    return data;
  }

  async readPort(portId: string): Promise<PortResource> {
    const { data } = await this.request(
      `/ports/${this.id(portId)}`,
      'PortResource',
    );
    if (data.id !== portId)
      throw new HttpContractError('Response belongs to another Port.');
    return this.sameNode(data);
  }

  async readCandidate(): Promise<CandidateSnapshot> {
    const { data, response } = await this.request(
      '/candidate',
      'CandidateResource',
    );
    return {
      candidate: this.sameNode(data),
      etag: strongEtag(response.headers.get('ETag')),
    };
  }

  async mutateCandidate(
    command: CandidateMutation,
    snapshot: CandidateSnapshot,
  ): Promise<CandidateWriteResult> {
    // Validate before sending. No requestId, ETag or identity is synthesized here.
    const submitted = this.check('CandidateMutation', structuredClone(command));
    const reviewed = structuredClone(snapshot);
    this.sameNode(this.check('CandidateResource', reviewed.candidate));
    strongEtag(reviewed.etag);
    if (reviewed.candidate.lockedByTransactionId)
      throw new HttpContractError('An active transaction owns this Candidate.');
    const requestId = submitted.requestId;
    try {
      const { data, response } = await this.request(
        '/candidate',
        'CandidateResource',
        { method: 'PATCH', command: submitted, etag: reviewed.etag },
      );
      this.sameNode(data);
      if (data.id !== reviewed.candidate.id)
        throw new HttpContractError('Response belongs to another Candidate.');
      return {
        kind: 'accepted',
        snapshot: {
          candidate: data,
          etag: strongEtag(response.headers.get('ETag')),
        },
      };
    } catch (error) {
      if (
        error instanceof ApiProblemError &&
        error.problem.commandEffect === 'not-started'
      )
        return { kind: 'rejected', problem: error.problem };
      // Timeout, abort, malformed JSON or lost reply may follow a durable stage.
      // Keep this requestId, read its ledger and refresh Candidate; never retry here.
      return { kind: 'unknown', requestId, nodeId: this.nodeId };
    }
  }

  async validateCandidate(
    command: ValidationRequest,
  ): Promise<ValidationWriteResult> {
    const submitted = this.check('ValidationRequest', structuredClone(command));
    try {
      const { data } = await this.request(
        '/validations',
        'ValidationResource',
        {
          method: 'POST',
          command: submitted,
          status: 202,
        },
      );
      this.validationResource(data);
      if (
        data.requestId !== submitted.requestId ||
        data.candidateId !== submitted.candidateId ||
        data.candidateRevision !== submitted.expectedCandidateRevision ||
        data.generation !== submitted.expectedGeneration
      )
        throw new HttpContractError('Mismatched accepted validation.');
      return { kind: 'accepted', validation: data };
    } catch (error) {
      if (
        error instanceof ApiProblemError &&
        error.problem.commandEffect === 'not-started'
      )
        return { kind: 'rejected', problem: error.problem };
      return {
        kind: 'unknown',
        requestId: submitted.requestId,
        nodeId: this.nodeId,
      };
    }
  }

  async readValidation(validationId: string): Promise<ValidationResource> {
    const { data } = await this.request(
      `/validations/${this.id(validationId)}`,
      'ValidationResource',
    );
    if (data.id !== validationId)
      throw new HttpContractError('Response belongs to another validation.');
    return this.validationResource(data);
  }

  async readValidationJob(
    validation: ValidationResource,
  ): Promise<JobResource> {
    const { data } = await this.request(
      `/jobs/${this.id(validation.jobId)}`,
      'JobResource',
    );
    if (
      data.id !== validation.jobId ||
      data.kind !== 'validation' ||
      data.correlationId !== validation.requestId
    )
      throw new HttpContractError(
        'Response belongs to another validation job.',
      );
    return this.sameNode(data);
  }

  async startSafeApply(
    command: StartSafeApplyRequest,
  ): ReturnType<ChangeControlGateway['startSafeApply']> {
    const submitted = this.check(
      'StartSafeApplyRequest',
      structuredClone(command),
    );
    try {
      const { data } = await this.request(
        '/transactions',
        'TransactionResource',
        { method: 'POST', command: submitted, status: 202 },
      );
      this.sameNode(data);
      if (
        data.requestId !== submitted.requestId ||
        data.candidateId !== submitted.candidateId ||
        data.candidateRevision !== submitted.expectedCandidateRevision
      )
        throw new HttpContractError('Mismatched accepted transaction.');
      return { kind: 'accepted', transaction: data };
    } catch (error) {
      if (error instanceof ApiProblemError)
        return { kind: 'problem', problem: error.problem };
      throw error;
    }
  }

  async readRequest(requestId: string): Promise<RequestRecord | null> {
    try {
      const { data } = await this.request(
        `/requests/${this.id(requestId)}`,
        'RequestRecord',
      );
      if (data.requestId !== requestId)
        throw new HttpContractError('Response belongs to another request.');
      return this.sameNode(data);
    } catch (error) {
      if (
        error instanceof ApiProblemError &&
        error.problem.status === 404 &&
        error.problem.code === 'NOT_FOUND'
      )
        return null;
      throw error;
    }
  }

  async readTransaction(transactionId: string): Promise<TransactionResource> {
    const { data } = await this.request(
      `/transactions/${this.id(transactionId)}`,
      'TransactionResource',
    );
    if (data.id !== transactionId)
      throw new HttpContractError('Response belongs to another transaction.');
    return this.sameNode(data);
  }

  async decideSafeApply(
    transactionId: string,
    command: DecisionRequest,
  ): Promise<TransactionWriteResult<TransactionResource>> {
    const submitted = this.check('DecisionRequest', structuredClone(command));
    try {
      const { data } = await this.request(
        `/transactions/${this.id(transactionId)}/decisions`,
        'TransactionResource',
        { method: 'POST', command: submitted, status: 202 },
      );
      this.sameNode(data);
      if (
        data.id !== transactionId ||
        data.sequence < submitted.expectedTransactionSequence
      )
        throw new HttpContractError(
          'Decision response belongs to another transaction or version.',
        );
      return { kind: 'accepted', resource: data };
    } catch (error) {
      if (
        error instanceof ApiProblemError &&
        error.problem.commandEffect === 'not-started'
      )
        return { kind: 'rejected', problem: error.problem };
      return { kind: 'unknown' };
    }
  }
  async reconcileTransaction(
    transactionId: string,
    command: ReconciliationRequest,
  ): Promise<TransactionWriteResult<JobResource>> {
    const submitted = this.check(
      'ReconciliationRequest',
      structuredClone(command),
    );
    try {
      const { data } = await this.request(
        `/transactions/${this.id(transactionId)}/reconciliations`,
        'JobResource',
        { method: 'POST', command: submitted, status: 202 },
      );
      this.sameNode(data);
      if (
        data.transactionId !== transactionId ||
        data.correlationId !== submitted.requestId ||
        data.kind !== 'reconciliation'
      )
        throw new HttpContractError('Mismatched reconciliation job.');
      return { kind: 'accepted', resource: data };
    } catch (error) {
      if (
        error instanceof ApiProblemError &&
        error.problem.commandEffect === 'not-started'
      )
        return { kind: 'rejected', problem: error.problem };
      return { kind: 'unknown' };
    }
  }
  async readJob(jobId: string): Promise<JobResource> {
    const { data } = await this.request(
      `/jobs/${this.id(jobId)}`,
      'JobResource',
    );
    if (data.id !== jobId) throw new HttpContractError('Mismatched job.');
    return this.sameNode(data);
  }
  async readTransactionJob(
    transaction: TransactionResource,
  ): Promise<JobResource> {
    const data = await this.readJob(transaction.jobId);
    if (
      data.kind !== 'safe-apply' ||
      data.transactionId !== transaction.id ||
      data.correlationId !== transaction.correlationId
    )
      throw new HttpContractError('Mismatched Safe Apply job.');
    return data;
  }
  async readEvidence(
    transactionId: string,
    cursor?: string,
  ): Promise<EvidencePage> {
    const query = new URLSearchParams({
      transactionId: this.check('Id', transactionId),
    });
    if (cursor) query.set('cursor', cursor);
    const { data } = await this.request(`/evidence?${query}`, 'EvidencePage');
    for (const item of data.items) {
      this.sameNode(item);
      if (item.transactionId !== transactionId)
        throw new HttpContractError('Evidence belongs to another transaction.');
    }
    return data;
  }
}
