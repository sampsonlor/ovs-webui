// Editable source for the first Ports -> Candidate -> Safe Apply integration slice.
// Generate the reviewable OpenAPI document and TypeScript types with pnpm contracts:generate.
// This is a proposed application contract, not a frozen upstream architecture or an OVSDB API.
const ref = (name) => ({ $ref: `#/components/schemas/${name}` });
const str = (extra = {}) => ({ type: 'string', ...extra });
const en = (...values) => ({ type: 'string', enum: values });
const integer = (minimum = 0, maximum = Number.MAX_SAFE_INTEGER) => ({
  type: 'integer',
  minimum,
  maximum,
});
const array = (items, extra = {}) => ({ type: 'array', items, ...extra });
const object = (properties, optional = []) => ({
  type: 'object',
  additionalProperties: false,
  properties,
  required: Object.keys(properties).filter((key) => !optional.includes(key)),
});
const nullable = (schema) => ({ anyOf: [schema, { type: 'null' }] });
const id = () => ref('Id');
const revision = () => ref('Revision');
const date = () => str({ format: 'date-time' });
const vlanIds = (minItems = 0) =>
  array(integer(1, 4094), { minItems, maxItems: 4094, uniqueItems: true });
const reason = () => str({ minLength: 1, maxLength: 2000, pattern: '\\S' });

export const schemas = {
  Id: str({
    minLength: 1,
    maxLength: 128,
    pattern: '^[A-Za-z0-9][A-Za-z0-9._:-]*$',
  }),
  Revision: str({
    minLength: 1,
    maxLength: 128,
    description:
      'Opaque equality token. Never increment, sort, or interpret as next_cfg / cur_cfg.',
  }),
  VlanIntent: {
    description:
      'Narrow editable subset. Explicit allowed VLANs only. An empty OVS trunks set means ALL VLANs, never none; existing unsupported/all-VLAN configuration remains observable but is not silently normalized into this editor.',
    oneOf: [
      object({
        mode: en('access'),
        tag: integer(1, 4094),
        trunks: { ...vlanIds(), maxItems: 0 },
      }),
      object({ mode: en('trunk'), tag: { type: 'null' }, trunks: vlanIds(1) }),
      object({
        mode: en('native-tagged'),
        tag: integer(1, 4094),
        trunks: vlanIds(1),
      }),
    ],
  },
  NativeVlan: object({
    mode: nullable(str()),
    tag: nullable(integer(0, 4095)),
    trunks: array(integer(0, 4095), { uniqueItems: true, maxItems: 4096 }),
  }),
  VlanObservation: {
    oneOf: [
      object({
        availability: en('known'),
        value: ref('VlanIntent'),
        native: ref('NativeVlan'),
      }),
      object({
        availability: en('unsupported'),
        value: { type: 'null' },
        native: ref('NativeVlan'),
        reason: str(),
      }),
      object({
        availability: en('unknown', 'unavailable'),
        value: { type: 'null' },
        native: { type: 'null' },
        reason: str(),
      }),
    ],
  },
  PortResource: object({
    id: id(),
    nodeId: id(),
    name: str(),
    bridgeId: id(),
    interfaces: array(object({ id: id(), name: str() }), { minItems: 1 }),
    kind: en('single', 'bond'),
    scope: en('manage', 'basic-manage', 'observe'),
    authority: en('ovs', 'external', 'unknown'),
    provider: str(),
    configuration: ref('VlanObservation'),
    linkState: en('up', 'down', 'unknown'),
    linkReason: nullable(str()),
    speedMbps: nullable(integer()),
    observedAt: date(),
    generation: revision(),
    writableFields: array(en('vlan'), { uniqueItems: true }),
  }),
  PortsPage: object({
    nodeId: id(),
    generation: revision(),
    observedAt: date(),
    snapshotId: id(),
    availability: en('complete', 'degraded'),
    items: array(ref('PortResource')),
    warnings: array(str()),
    nextCursor: nullable(str()),
  }),
  VlanChange: object({
    id: id(),
    portId: id(),
    base: ref('VlanIntent'),
    mine: ref('VlanIntent'),
  }),
  VlanConflict: object({
    intentId: id(),
    portId: id(),
    field: en('vlan'),
    base: ref('VlanIntent'),
    current: ref('VlanIntent'),
    mine: ref('VlanIntent'),
  }),
  CandidateResource: object({
    id: id(),
    nodeId: id(),
    revision: revision(),
    baseGeneration: revision(),
    currentGeneration: revision(),
    freshness: en('current', 'stale', 'conflict'),
    intents: array(ref('VlanChange'), { maxItems: 1 }),
    conflicts: array(ref('VlanConflict')),
    conflictSnapshotId: nullable(id()),
    lockedByTransactionId: nullable(id()),
    updatedAt: date(),
  }),
  CandidateMutation: {
    oneOf: [
      object({
        requestId: id(),
        operation: en('set-vlan'),
        portId: id(),
        mine: ref('VlanIntent'),
        expectedGeneration: revision(),
      }),
      object({ requestId: id(), operation: en('discard') }),
      object({
        requestId: id(),
        operation: en('rebase'),
        currentGeneration: revision(),
        conflictSnapshotId: nullable(id()),
        resolutions: array(
          object({ intentId: id(), choice: en('current', 'mine') }),
          { maxItems: 1 },
        ),
      }),
    ],
  },
  ValidationRequest: object({
    requestId: id(),
    candidateId: id(),
    expectedCandidateRevision: revision(),
    expectedGeneration: revision(),
  }),
  ValidationCheck: object({
    code: str(),
    state: en('pass', 'warning', 'block', 'unknown'),
    message: str(),
    portId: nullable(id()),
  }),
  SafetyPlan: object({
    checkpoint: en('available', 'unavailable', 'unknown'),
    connectivityProbe: en('available', 'unavailable', 'unknown'),
    compareBeforeRollback: en('available', 'unavailable', 'unknown'),
    confirmationWindowSeconds: integer(1, 86400),
    reauthenticationRequired: { type: 'boolean' },
  }),
  ValidationResource: object({
    id: id(),
    jobId: id(),
    candidateId: id(),
    candidateRevision: revision(),
    generation: revision(),
    policyRevision: revision(),
    status: en('pending', 'running', 'passed', 'blocked', 'expired'),
    expiresAt: nullable(date()),
    checks: array(ref('ValidationCheck')),
    diff: array(ref('VlanChange')),
    safetyPlan: nullable(ref('SafetyPlan')),
  }),
  StartSafeApplyRequest: object(
    {
      requestId: id(),
      candidateId: id(),
      expectedCandidateRevision: revision(),
      expectedGeneration: revision(),
      validationId: id(),
      reason: reason(),
      reauthenticationProof: str({
        minLength: 1,
        maxLength: 4096,
        writeOnly: true,
        description:
          'Opaque, short-lived proof from the existing authentication service. Never log or persist in a recovery hint.',
      }),
    },
    ['reauthenticationProof'],
  ),
  TransactionEvidence: object({
    databaseCommit: en('pending', 'committed', 'not-committed', 'unknown'),
    daemonApply: en('pending', 'applied', 'failed', 'unknown'),
    health: en('pending', 'passed', 'failed', 'unknown'),
    checkpoint: en('pending', 'ready', 'unavailable', 'unknown'),
    desiredGeneration: nullable(revision()),
    observedGeneration: nullable(revision()),
    nextCfg: nullable(str({ pattern: '^[0-9]+$' })),
    curCfg: nullable(str({ pattern: '^[0-9]+$' })),
  }),
  TransactionResource: object({
    id: id(),
    nodeId: id(),
    requestId: id(),
    candidateId: id(),
    candidateRevision: revision(),
    jobId: id(),
    correlationId: id(),
    sequence: integer(1),
    serverTime: date(),
    knowledge: en('current', 'outcome-unknown'),
    phase: en(
      'queued',
      'preflight',
      'committing',
      'applying',
      'awaiting-confirmation',
      'rolling-back',
      'settled',
    ),
    outcome: en(
      'unknown',
      'applied',
      'not-applied',
      'degraded',
      'needs-attention',
    ),
    safeApply: en(
      'not-started',
      'armed',
      'awaiting-confirmation',
      'confirming',
      'confirmed',
      'rollback-requested',
      'rolled-back',
      'rollback-conflict',
    ),
    confirmationDeadline: nullable(date()),
    locksCandidate: { type: 'boolean' },
    allowedActions: array(en('read', 'reconcile', 'confirm', 'rollback'), {
      uniqueItems: true,
    }),
    evidence: ref('TransactionEvidence'),
  }),
  DecisionRequest: object(
    {
      requestId: id(),
      expectedTransactionSequence: integer(1),
      decision: en('confirm', 'rollback'),
      reason: reason(),
      reauthenticationProof: str({
        minLength: 1,
        maxLength: 4096,
        writeOnly: true,
      }),
    },
    ['reauthenticationProof'],
  ),
  ReconciliationRequest: object({ requestId: id() }),
  JobResource: object({
    id: id(),
    nodeId: id(),
    kind: en('validation', 'safe-apply', 'reconciliation', 'drift-read'),
    state: en(
      'queued',
      'running',
      'succeeded',
      'failed',
      'cancelled',
      'unknown',
    ),
    transactionId: nullable(id()),
    correlationId: id(),
    updatedAt: date(),
    message: str(),
  }),
  Problem: object({
    type: str({ format: 'uri-reference' }),
    title: str(),
    status: integer(400, 599),
    detail: str(),
    instance: str({ format: 'uri-reference' }),
    code: en(
      'UNAUTHENTICATED',
      'FORBIDDEN',
      'REAUTH_REQUIRED',
      'ETAG_MISMATCH',
      'PRECONDITION_REQUIRED',
      'CANDIDATE_STALE',
      'FIELD_CONFLICT',
      'VALIDATION_EXPIRED',
      'VALIDATION_BLOCKED',
      'TRANSACTION_ACTIVE',
      'DECISION_EXPIRED',
      'TRANSACTION_VERSION_CHANGED',
      'IDEMPOTENCY_MISMATCH',
      'UNSUPPORTED_CONFIGURATION',
      'INVALID_INTENT',
      'PROVIDER_UNAVAILABLE',
      'OUTCOME_UNKNOWN',
      'NOT_FOUND',
      'INTERNAL_ERROR',
    ),
    requestId: id(),
    correlationId: id(),
    commandEffect: en('not-started', 'unknown'),
    transactionId: nullable(id()),
    candidateRevision: nullable(revision()),
  }),
  RequestRecord: object({
    requestId: id(),
    nodeId: id(),
    operation: en(
      'candidate',
      'validation',
      'safe-apply',
      'decision',
      'reconciliation',
      'drift-read',
    ),
    state: en('recorded', 'accepted', 'rejected'),
    recordedAt: date(),
    transactionId: nullable(id()),
    jobId: nullable(id()),
    validationId: nullable(id()),
    candidateRevision: nullable(revision()),
    problem: nullable(ref('Problem')),
  }),
  WorkspaceSnapshot: object({
    nodeId: id(),
    serverTime: date(),
    candidate: ref('CandidateResource'),
    activeTransactions: array(ref('TransactionResource')),
    pendingRequests: array(ref('RequestRecord')),
    nodeWriteBlocked: { type: 'boolean' },
    permissions: object({
      editCandidate: { type: 'boolean' },
      validate: { type: 'boolean' },
      startSafeApply: { type: 'boolean' },
    }),
  }),
  DriftReport: object({
    id: id(),
    nodeId: id(),
    observedAt: date(),
    desiredGeneration: revision(),
    observedGeneration: nullable(revision()),
    status: en('in-sync', 'drift', 'unknown'),
    items: array(
      object({
        portId: id(),
        desired: ref('VlanIntent'),
        observed: ref('VlanObservation'),
      }),
    ),
  }),
  EvidenceEntry: object({
    id: id(),
    kind: en('audit', 'event', 'job', 'health'),
    occurredAt: date(),
    nodeId: id(),
    transactionId: id(),
    requestId: id(),
    correlationId: id(),
    objectId: id(),
    code: str(),
    summary: str(),
  }),
  EvidencePage: object({
    items: array(ref('EvidenceEntry')),
    nextCursor: nullable(str()),
  }),
};

const pathParameter = (name) => ({
  name,
  in: 'path',
  required: true,
  schema: id(),
});
const query = (name, required = false) => ({
  name,
  in: 'query',
  required,
  schema: str({ minLength: 1 }),
});
const json = (name) => ({ 'application/json': { schema: ref(name) } });
const response = (name, description = 'Authoritative resource snapshot.') => ({
  description,
  content: json(name),
});
const errors = {
  default: {
    description:
      'RFC 9457 problem. A failed transport or unknown commandEffect never proves that a write was not accepted.',
    content: { 'application/problem+json': { schema: ref('Problem') } },
  },
};
const writeHeaders = [
  {
    name: 'Idempotency-Key',
    in: 'header',
    required: true,
    schema: id(),
    description:
      'Must equal body.requestId. Durable per-principal/node/method/path/body binding. Do not rotate after a timeout.',
  },
  {
    name: 'X-CSRF-Token',
    in: 'header',
    required: true,
    schema: str({ minLength: 1 }),
    description:
      'Validated against the authenticated session; exact authentication integration remains an upstream review item.',
  },
];
const get = (operationId, name, parameters = [], description = '') => ({
  operationId,
  description,
  parameters,
  responses: { 200: response(name), ...errors },
});
const post = (
  operationId,
  input,
  output,
  parameters = [],
  description = '',
) => ({
  operationId,
  description,
  parameters: [...parameters, ...writeHeaders],
  requestBody: { required: true, content: json(input) },
  responses: {
    202: response(
      output,
      'Accepted for asynchronous processing. This is not a success/confirmation verdict.',
    ),
    ...errors,
  },
});

export const contract = {
  openapi: '3.1.1',
  info: {
    title: 'OVS WebUI core workflow contract',
    version: '0.1.0-draft',
    description:
      'Review proposal for the Ports/VLAN integration slice on one node. Candidate and request ledger are server-persistent and per authenticated principal. Standard/Expert is a presentation preference and is not an authorization input. No implementation endpoints are enabled by this document.',
  },
  servers: [
    {
      url: '/api/v1',
      description:
        'Proposed same-origin application API; not the Sites publishing API and not an OVSDB endpoint.',
    },
  ],
  security: [{ sessionCookie: [] }],
  'x-review-status':
    'Draft for integration review; not an approved Architecture / Phase 1 / P1 gate.',
  paths: {
    '/workspace': {
      get: get(
        'readWorkspace',
        'WorkspaceSnapshot',
        [],
        "Consistent bootstrap after login, reload or reconnect. Resolve active/unresolved requests before enabling any new write. Other users' private candidates are never disclosed; nodeWriteBlocked represents any remaining node admission lock.",
      ),
    },
    '/ports': {
      get: get(
        'listPorts',
        'PortsPage',
        [query('cursor'), query('search')],
        'Cursors pin a snapshot. Complete with zero items means empty; degraded is partial evidence; provider unavailable returns a Problem, never a fabricated empty list.',
      ),
    },
    '/ports/{portId}': {
      get: get('readPort', 'PortResource', [pathParameter('portId')]),
    },
    '/candidate': {
      get: {
        ...get(
          'readCandidate',
          'CandidateResource',
          [],
          "Current principal's durable candidate, including an empty intent list.",
        ),
        responses: {
          200: {
            ...response('CandidateResource'),
            headers: {
              ETag: {
                required: true,
                schema: str(),
                description:
                  'Strong quoted entity tag for the entire candidate representation. Changes whenever represented freshness or intent changes.',
              },
            },
          },
          ...errors,
        },
      },
      patch: {
        operationId: 'mutateCandidate',
        description:
          'Typed candidate command; no running OVS write. If-Match is the strong ETag read from this same /candidate resource. Stale tag: 412; missing tag: 428. Rebase checks the current generation and conflict snapshot atomically, invalidates validation, and never applies.',
        parameters: [
          ...writeHeaders,
          {
            name: 'If-Match',
            in: 'header',
            required: true,
            schema: str({ pattern: '^"[^"\\r\\n]+"$' }),
            description: 'No wildcard and no weak tag.',
          },
        ],
        requestBody: { required: true, content: json('CandidateMutation') },
        responses: {
          200: {
            ...response('CandidateResource'),
            headers: { ETag: { required: true, schema: str() } },
          },
          ...errors,
        },
      },
    },
    '/validations': {
      post: post(
        'validateCandidate',
        'ValidationRequest',
        'ValidationResource',
        [],
        'Validation captures candidate revision, config generation, policy revision and expiry. A previous passed result is not permission to apply a different snapshot.',
      ),
    },
    '/validations/{validationId}': {
      get: get('readValidation', 'ValidationResource', [
        pathParameter('validationId'),
      ]),
    },
    '/transactions': {
      post: post(
        'startSafeApply',
        'StartSafeApplyRequest',
        'TransactionResource',
        [],
        'Atomically persist request admission, immutable candidate snapshot and node admission lock before any OVS mutation. Recheck authorization, ownership, revision, generation, validation, reauthentication and actual safety resources. The server owns the confirmation deadline and rollback scheduler.',
      ),
    },
    '/transactions/{transactionId}': {
      get: get(
        'readTransaction',
        'TransactionResource',
        [pathParameter('transactionId')],
        'Database commit, daemon apply, health and user confirmation are separate facts. GET never resubmits the configuration.',
      ),
    },
    '/transactions/{transactionId}/decisions': {
      post: post(
        'decideSafeApply',
        'DecisionRequest',
        'TransactionResource',
        [pathParameter('transactionId')],
        "Compare expectedTransactionSequence, state and server deadline atomically. A late confirm loses to expiry. Protected rollback compares only the owned write set with this transaction's after-image and never overwrites an external change.",
      ),
    },
    '/transactions/{transactionId}/reconciliations': {
      post: post(
        'reconcileTransaction',
        'ReconciliationRequest',
        'JobResource',
        [pathParameter('transactionId')],
        'Create an observation job referencing the original transaction. May update evidence/metadata; must not replay configuration or invent a new apply transaction. The client cannot select an outcome.',
      ),
    },
    '/requests/{requestId}': {
      get: get(
        'readRequest',
        'RequestRecord',
        [pathParameter('requestId')],
        'Recover a lost acceptance reply before a transaction ID is known. 404 means the ledger cannot currently provide a record; it does NOT prove Not Applied. Unresolved admissions must survive restarts and must not expire out of the ledger.',
      ),
    },
    '/jobs/{jobId}': {
      get: get(
        'readJob',
        'JobResource',
        [pathParameter('jobId')],
        'Job success describes completion of the job task, not necessarily applied configuration, healthy traffic, or confirmed Safe Apply.',
      ),
    },
    '/drift': {
      get: get(
        'readDrift',
        'DriftReport',
        [],
        'Desired-versus-observed evidence. Distinct from candidate freshness.',
      ),
    },
    '/drift-observations': {
      post: post(
        'observeDrift',
        'ReconciliationRequest',
        'JobResource',
        [],
        'Refresh observations only. No apply, overwrite, or candidate rebase.',
      ),
    },
    '/evidence': {
      get: get(
        'readEvidence',
        'EvidencePage',
        [query('transactionId', true), query('cursor')],
        'Authorization-scoped evidence for the original transaction. Never expose credentials, reauthentication proofs or unrestricted native dumps.',
      ),
    },
  },
  components: {
    schemas,
    securitySchemes: {
      sessionCookie: {
        type: 'apiKey',
        in: 'cookie',
        name: 'ovs_session',
        description:
          'Provisional same-origin session binding, requiring Secure/HttpOnly/SameSite and CSRF checks on writes. Cookie name, issuer, login and reauthentication endpoints must align with the approved auth service before a live adapter is enabled. This is not a replacement authentication implementation.',
      },
    },
  },
};
