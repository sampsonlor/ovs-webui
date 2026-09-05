import { randomUUID, createHash } from 'node:crypto';

const id = (prefix) => `${prefix}-${randomUUID()}`;
const terminal = (status) => !['pending', 'running'].includes(status);
const safetyKeys = ['checkpoint', 'connectivityProbe', 'compareBeforeRollback'];

// Validation performs no configuration write. Safe Apply separately captures its
// own synthetic checkpoint and rechecks these capabilities at admission and commit.
export class CoreLabValidation {
  constructor(store, { clock = Date.now } = {}) {
    this.store = store;
    this.clock = clock;
    store.db.exec(`CREATE TABLE IF NOT EXISTS validations (
      principal TEXT NOT NULL, id TEXT NOT NULL, candidate_id TEXT NOT NULL,
      body TEXT NOT NULL, job TEXT NOT NULL, PRIMARY KEY (principal, id));`);
    if (!store.meta('validationPolicy'))
      store.setMeta('validationPolicy', {
        revision: id('policy'),
        safety: 'unavailable',
      });
  }
  iso() {
    return new Date(this.clock()).toISOString();
  }
  policyChange(safety) {
    const policy = this.store.meta('validationPolicy');
    this.store.setMeta('validationPolicy', {
      ...policy,
      ...(safety ? { safety } : {}),
      revision: id('policy'),
    });
  }
  start(session, command, key) {
    const s = this.store;
    if (!s.ajv.getSchema('#/components/schemas/ValidationRequest')(command))
      return s.problem(
        'INVALID_INTENT',
        422,
        'Use a typed Validation request.',
        key,
      );
    const requestId = command.requestId;
    if (key !== requestId)
      return s.problem(
        'IDEMPOTENCY_MISMATCH',
        409,
        'Idempotency-Key must match requestId.',
        requestId,
      );
    const fingerprint = createHash('sha256')
      .update(
        JSON.stringify([
          session.nodeId,
          'POST',
          '/validations',
          requestId,
          command.candidateId,
          command.expectedCandidateRevision,
          command.expectedGeneration,
        ]),
      )
      .digest('hex');
    return s.transaction(() => {
      if (!s.canEdit(session.principal))
        return s.problem(
          'FORBIDDEN',
          403,
          'This user no longer has validation permission.',
          requestId,
        );
      const prior = s.db
        .prepare(
          'SELECT * FROM requests WHERE principal = ? AND request_id = ?',
        )
        .get(session.principal, requestId);
      if (prior)
        return prior.fingerprint === fingerprint
          ? { status: prior.status, body: JSON.parse(prior.body) }
          : s.problem(
              'IDEMPOTENCY_MISMATCH',
              409,
              'The original request has different content or operation.',
              requestId,
            );
      const candidate = s.candidate(session.principal);
      const reject = (code, status, detail) =>
        s.problem(code, status, detail, requestId);
      let result;
      if (
        candidate.id !== command.candidateId ||
        session.nodeId !== candidate.nodeId
      )
        result = reject(
          'NOT_FOUND',
          404,
          'Candidate does not belong to this workspace.',
        );
      else if (candidate.revision !== command.expectedCandidateRevision)
        result = reject(
          'ETAG_MISMATCH',
          412,
          'Candidate changed before validation admission. Refresh and review it.',
        );
      else if (candidate.currentGeneration !== command.expectedGeneration)
        result = reject(
          'CANDIDATE_STALE',
          409,
          'Configuration generation changed before validation admission.',
        );
      else {
        const resource = {
          id: id('validation'),
          nodeId: candidate.nodeId,
          requestId,
          jobId: id('job'),
          candidateId: candidate.id,
          candidateRevision: candidate.revision,
          generation: candidate.currentGeneration,
          policyRevision: s.meta('validationPolicy').revision,
          status: 'pending',
          expiresAt: new Date(this.clock() + 120_000).toISOString(),
          checks: [],
          diff: structuredClone(candidate.intents),
          safetyPlan: null,
        };
        const job = {
          id: resource.jobId,
          nodeId: candidate.nodeId,
          kind: 'validation',
          state: 'queued',
          transactionId: null,
          correlationId: requestId,
          updatedAt: this.iso(),
          message: 'Validation accepted. The local server owns this job.',
        };
        s.db
          .prepare('INSERT INTO validations VALUES (?, ?, ?, ?, ?)')
          .run(
            session.principal,
            resource.id,
            candidate.id,
            JSON.stringify(resource),
            JSON.stringify(job),
          );
        result = { status: 202, body: resource };
      }
      const accepted = result.status === 202;
      const record = {
        requestId,
        nodeId: session.nodeId,
        operation: 'validation',
        state: accepted ? 'accepted' : 'rejected',
        recordedAt: this.iso(),
        transactionId: null,
        jobId: accepted ? result.body.jobId : null,
        validationId: accepted ? result.body.id : null,
        candidateRevision: candidate.revision,
        problem: accepted ? null : result.body,
      };
      s.db
        .prepare('INSERT INTO requests VALUES (?, ?, ?, ?, ?, ?, ?)')
        .run(
          session.principal,
          requestId,
          fingerprint,
          result.status,
          JSON.stringify(result.body),
          null,
          JSON.stringify(record),
        );
      return result;
    });
  }
  save(principal, resource, job) {
    this.store.db
      .prepare(
        'UPDATE validations SET body = ?, job = ? WHERE principal = ? AND id = ?',
      )
      .run(
        JSON.stringify(resource),
        JSON.stringify(job),
        principal,
        resource.id,
      );
  }
  expiration(principal, resource) {
    const s = this.store;
    const candidate = s.candidate(principal);
    if (
      candidate.id !== resource.candidateId ||
      candidate.revision !== resource.candidateRevision
    )
      return 'Candidate revision changed. Review the new diff and validate again.';
    if (candidate.currentGeneration !== resource.generation)
      return 'Configuration generation changed. Review Stale / Conflict before validating again.';
    if (s.meta('validationPolicy').revision !== resource.policyRevision)
      return 'Validation policy or capabilities changed. A fresh validation is required.';
    if (!s.canEdit(principal)) return 'Validation permission was revoked.';
    if (!resource.expiresAt || Date.parse(resource.expiresAt) <= this.clock())
      return 'The server validation window expired. Validate again.';
    return null;
  }
  expire(principal, resource, job, reason) {
    resource.status = 'expired';
    resource.checks.push({
      code: 'VALIDATION_EXPIRED',
      state: 'block',
      message: reason,
      portId: null,
    });
    if (['queued', 'running'].includes(job.state)) {
      job.state = 'cancelled';
      job.updatedAt = this.iso();
      job.message = 'The validation snapshot expired before completion.';
    }
    this.save(principal, resource, job);
  }
  read(principal, validationId) {
    return this.store.transaction(() => {
      const row = this.store.db
        .prepare('SELECT * FROM validations WHERE principal = ? AND id = ?')
        .get(principal, validationId);
      if (!row)
        return this.store.problem(
          'NOT_FOUND',
          404,
          'Validation not found in this user workspace.',
        );
      const resource = JSON.parse(row.body);
      const job = JSON.parse(row.job);
      const reason =
        resource.status === 'expired'
          ? null
          : this.expiration(principal, resource);
      if (reason) this.expire(principal, resource, job, reason);
      return { status: 200, body: resource };
    });
  }
  latest(principal) {
    const row = this.store.db
      .prepare(
        'SELECT id FROM validations WHERE principal = ? ORDER BY rowid DESC LIMIT 1',
      )
      .get(principal);
    return row ? this.read(principal, row.id).body : null;
  }
  job(principal, jobId) {
    const row = this.store.db
      .prepare(
        "SELECT id FROM validations WHERE principal = ? AND json_extract(job, '$.id') = ?",
      )
      .get(principal, jobId);
    if (!row)
      return this.store.problem(
        'NOT_FOUND',
        404,
        'Job not found in this user workspace.',
      );
    this.read(principal, row.id);
    return {
      status: 200,
      body: JSON.parse(
        this.store.db
          .prepare('SELECT job FROM validations WHERE principal = ? AND id = ?')
          .get(principal, row.id).job,
      ),
    };
  }
  // A server-owned worker calls this independently of browser reads. Queued and
  // running rows remain recoverable after a process restart. Each step is atomic.
  tick() {
    const s = this.store;
    return s.transaction(() => {
      const rows = s.db
        .prepare(
          "SELECT * FROM validations WHERE json_extract(body, '$.status') IN ('pending', 'running') ORDER BY rowid LIMIT 32",
        )
        .all();
      for (const row of rows) {
        const resource = JSON.parse(row.body);
        const job = JSON.parse(row.job);
        const reason = this.expiration(row.principal, resource);
        if (reason) {
          this.expire(row.principal, resource, job, reason);
          continue;
        }
        if (resource.status === 'pending') {
          resource.status = 'running';
          job.state = 'running';
          job.updatedAt = this.iso();
          job.message =
            'Checking the captured Candidate and synthetic safety capabilities.';
          this.save(row.principal, resource, job);
          continue;
        }
        const candidate = s.candidate(row.principal);
        const checks = [];
        const check = (code, ok, message, portId = null) =>
          checks.push({ code, state: ok ? 'pass' : 'block', message, portId });
        check(
          'CANDIDATE_NOT_EMPTY',
          resource.diff.length > 0,
          resource.diff.length
            ? 'Candidate has a VLAN change.'
            : 'Stage a change before validating.',
        );
        check(
          'CANDIDATE_CURRENT',
          candidate.freshness === 'current',
          candidate.freshness === 'current'
            ? 'Candidate base matches current configuration.'
            : 'Resolve Conflict / Stale and rebase before validation.',
        );
        check(
          'PROVIDER_AVAILABLE',
          s.meta('providerAvailable'),
          s.meta('providerAvailable')
            ? 'Synthetic inventory provider is available.'
            : 'Inventory provider is unavailable.',
        );
        check(
          'NODE_ADMISSION',
          !s.transactions.blocked() && !candidate.lockedByTransactionId,
          'No unresolved node operation or Candidate owner may block admission.',
        );
        for (const intent of resource.diff) {
          const port = s
            .meta('inventory')
            .items.find((item) => item.id === intent.portId);
          check(
            'PORT_WRITABLE',
            Boolean(
              port &&
              port.authority === 'ovs' &&
              port.scope !== 'observe' &&
              port.writableFields.includes('vlan') &&
              port.configuration.availability === 'known',
            ),
            'Port requires known native VLAN configuration and write authority.',
            intent.portId,
          );
          check(
            'VLAN_SUPPORTED',
            s.ajv.getSchema('#/components/schemas/VlanIntent')(intent.mine),
            'The requested VLAN value must match the supported typed intent.',
            intent.portId,
          );
        }
        const safety = s.meta('validationPolicy').safety;
        resource.safetyPlan = {
          checkpoint: safety,
          connectivityProbe: safety,
          compareBeforeRollback: safety,
          confirmationWindowSeconds: s.transactions.windowSeconds,
          reauthenticationRequired: false,
        };
        for (const key of safetyKeys)
          checks.push({
            code: `SAFETY_${key}`,
            state:
              safety === 'available'
                ? 'pass'
                : safety === 'unknown'
                  ? 'unknown'
                  : 'block',
            message:
              safety === 'available'
                ? `${key}: available for the local synthetic executor.`
                : `${key}: ${safety}; Safe Apply prerequisites are not satisfied.`,
            portId: null,
          });
        resource.checks = checks;
        resource.status = checks.some((item) =>
          ['block', 'unknown'].includes(item.state),
        )
          ? 'blocked'
          : 'passed';
        job.state = 'succeeded';
        job.updatedAt = this.iso();
        job.message = `Validation job completed; result ${resource.status}. No configuration was applied.`;
        this.save(row.principal, resource, job);
      }
    });
  }
  expireLatest(principal) {
    const latest = this.latest(principal);
    if (!latest || !terminal(latest.status)) return false;
    return this.store.transaction(() => {
      const row = this.store.db
        .prepare('SELECT job FROM validations WHERE principal = ? AND id = ?')
        .get(principal, latest.id);
      if (latest.status !== 'expired')
        this.expire(
          principal,
          latest,
          JSON.parse(row.job),
          'Validation expiry was simulated by the local review harness.',
        );
      return true;
    });
  }
}

export function startValidationWorker(
  store,
  onError = (error) =>
    process.stderr.write(`Validation worker: ${error.message}\n`),
) {
  const timer = setInterval(() => {
    try {
      store.validations.tick();
    } catch (error) {
      onError(error);
    }
  }, 200);
  timer.unref();
  return () => clearInterval(timer);
}
