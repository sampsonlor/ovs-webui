import { randomUUID, createHash } from 'node:crypto';

const id = (prefix) => `${prefix}-${randomUUID()}`;
const canonical = (value) =>
  JSON.stringify(value, (_key, item) =>
    item && typeof item === 'object' && !Array.isArray(item)
      ? Object.fromEntries(
          Object.entries(item).sort(([a], [b]) => a.localeCompare(b)),
        )
      : item,
  );
const same = (a, b) => canonical(a) === canonical(b);

// This executor owns only synthetic inventory in this same SQLite database.
// A real OVS provider cannot inherit its atomic commit/receipt guarantees.
export class CoreLabTransactions {
  constructor(
    store,
    { clock = Date.now, confirmationWindowSeconds = 90 } = {},
  ) {
    this.store = store;
    this.clock = clock;
    if (
      !Number.isInteger(confirmationWindowSeconds) ||
      confirmationWindowSeconds < 1 ||
      confirmationWindowSeconds > 86400
    )
      throw new Error('Invalid server confirmation window.');
    this.windowSeconds = confirmationWindowSeconds;
    store.db.exec(`
      CREATE TABLE IF NOT EXISTS safe_transactions (
        id TEXT PRIMARY KEY, principal TEXT NOT NULL, body TEXT NOT NULL, plan TEXT NOT NULL, job TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS node_admission (node_id TEXT PRIMARY KEY, transaction_id TEXT NOT NULL UNIQUE);
      CREATE TABLE IF NOT EXISTS transaction_evidence (
        id TEXT PRIMARY KEY, principal TEXT NOT NULL, transaction_id TEXT NOT NULL, body TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS reconciliation_jobs (
        id TEXT PRIMARY KEY, principal TEXT NOT NULL, transaction_id TEXT NOT NULL, body TEXT NOT NULL);
      CREATE INDEX IF NOT EXISTS idx_safe_transactions_principal ON safe_transactions(principal);
      CREATE INDEX IF NOT EXISTS idx_transaction_evidence_owner ON transaction_evidence(principal, transaction_id);
      PRAGMA optimize;`);
  }
  iso() {
    return new Date(this.clock()).toISOString();
  }
  blocked() {
    return Boolean(
      this.store.meta('nodeBlocked') ||
      this.store.db.prepare('SELECT 1 FROM node_admission LIMIT 1').get(),
    );
  }
  row(principal, transactionId) {
    const row = this.store.db
      .prepare('SELECT * FROM safe_transactions WHERE principal = ? AND id = ?')
      .get(principal, transactionId);
    return row
      ? {
          ...row,
          body: JSON.parse(row.body),
          plan: JSON.parse(row.plan),
          job: JSON.parse(row.job),
        }
      : null;
  }
  ownedMatches(row, image = 'mine') {
    const items = this.store.meta('inventory').items;
    return row.plan.diff.every((change) => {
      const port = items.find((item) => item.id === change.portId);
      return (
        port &&
        port.authority === 'ovs' &&
        port.scope !== 'observe' &&
        port.writableFields.includes('vlan') &&
        port.configuration.availability === 'known' &&
        same(port.configuration.value, change[image])
      );
    });
  }
  wire(row) {
    const tx = structuredClone(row.body);
    tx.serverTime = this.iso();
    tx.allowedActions = ['read'];
    if (this.store.canEdit(row.principal) && tx.locksCandidate) {
      tx.allowedActions.push('reconcile');
      if (
        tx.phase === 'awaiting-confirmation' &&
        tx.knowledge === 'current' &&
        tx.outcome === 'applied' &&
        Date.parse(tx.confirmationDeadline) > this.clock() &&
        tx.evidence.checkpoint === 'ready' &&
        this.store.meta('providerAvailable') &&
        this.store.meta('validationPolicy').safety === 'available' &&
        this.ownedMatches(row) &&
        this.store.meta('inventory').generation === row.plan.commitGeneration
      ) {
        tx.allowedActions.push('rollback');
        if (tx.evidence.health === 'passed') tx.allowedActions.push('confirm');
      }
    }
    return tx;
  }
  latest(principal) {
    const row = this.store.db
      .prepare(
        'SELECT id FROM safe_transactions WHERE principal = ? ORDER BY rowid DESC LIMIT 1',
      )
      .get(principal);
    return row ? this.wire(this.row(principal, row.id)) : null;
  }
  active(principal) {
    return this.store.db
      .prepare(
        "SELECT id FROM safe_transactions WHERE principal = ? AND json_extract(body, '$.locksCandidate') = 1",
      )
      .all(principal)
      .map(({ id }) => this.wire(this.row(principal, id)));
  }
  read(principal, transactionId) {
    const row = this.row(principal, transactionId);
    return row
      ? { status: 200, body: this.wire(row) }
      : this.store.problem(
          'NOT_FOUND',
          404,
          'Transaction not found in this workspace.',
        );
  }
  save(row, code, summary, requestId = row.body.requestId) {
    const tx = row.body;
    tx.allowedActions = this.wire(row).allowedActions;
    tx.sequence++;
    tx.serverTime = this.iso();
    row.job.updatedAt = this.iso();
    row.job.message = summary;
    this.store.db
      .prepare(
        'UPDATE safe_transactions SET body = ?, plan = ?, job = ? WHERE id = ?',
      )
      .run(
        JSON.stringify(tx),
        JSON.stringify(row.plan),
        JSON.stringify(row.job),
        tx.id,
      );
    this.evidenceEntry(row, code, summary, requestId);
  }
  evidenceEntry(row, code, summary, requestId = row.body.requestId) {
    const entry = {
      id: id('evidence'),
      kind: [
        'SAFE_APPLY_ACCEPTED',
        'CONFIRMED',
        'ROLLBACK_REQUESTED',
        'RECONCILIATION_REQUESTED',
      ].includes(code)
        ? 'audit'
        : 'event',
      occurredAt: this.iso(),
      nodeId: row.body.nodeId,
      transactionId: row.id,
      requestId,
      correlationId: row.body.correlationId,
      objectId: row.body.candidateId,
      code,
      summary,
    };
    this.store.db
      .prepare('INSERT INTO transaction_evidence VALUES (?, ?, ?, ?)')
      .run(entry.id, row.principal, row.id, JSON.stringify(entry));
  }
  job(principal, jobId) {
    const row = this.store.db
      .prepare(
        "SELECT job FROM safe_transactions WHERE principal = ? AND json_extract(job, '$.id') = ?",
      )
      .get(principal, jobId);
    const reconciliation = this.store.db
      .prepare(
        'SELECT body FROM reconciliation_jobs WHERE principal = ? AND id = ?',
      )
      .get(principal, jobId);
    return row || reconciliation
      ? { status: 200, body: JSON.parse(row?.job ?? reconciliation.body) }
      : null;
  }
  evidence(principal, transactionId, cursor) {
    if (!this.row(principal, transactionId))
      return this.store.problem(
        'NOT_FOUND',
        404,
        'Evidence not found in this workspace.',
      );
    if (cursor && !/^[1-9][0-9]{0,14}$/.test(cursor))
      return this.store.problem(
        'INVALID_INTENT',
        422,
        'Invalid evidence cursor.',
      );
    const rows = this.store.db
      .prepare(
        'SELECT rowid, body FROM transaction_evidence WHERE principal = ? AND transaction_id = ? AND rowid < ? ORDER BY rowid DESC LIMIT 51',
      )
      .all(
        principal,
        transactionId,
        cursor ? Number(cursor) : Number.MAX_SAFE_INTEGER,
      );
    return {
      status: 200,
      body: {
        items: rows.slice(0, 50).map((row) => JSON.parse(row.body)),
        nextCursor: rows.length > 50 ? String(rows[49].rowid) : null,
      },
    };
  }
  command(
    session,
    schema,
    command,
    key,
    path,
    operation,
    transactionId,
    perform,
  ) {
    const s = this.store;
    if (!s.ajv.getSchema(`#/components/schemas/${schema}`)(command))
      return s.problem('INVALID_INTENT', 422, `Use a typed ${schema}.`, key);
    if (key !== command.requestId)
      return s.problem(
        'IDEMPOTENCY_MISMATCH',
        409,
        'Idempotency-Key must match requestId.',
        command.requestId,
      );
    // No production reauthentication mechanism or proof storage exists in this lab.
    if (command.reauthenticationProof)
      return s.problem(
        'REAUTH_REQUIRED',
        401,
        'This local service cannot verify a production reauthentication proof.',
        command.requestId,
      );
    const fingerprint = createHash('sha256')
      .update(canonical([session.nodeId, 'POST', path, command]))
      .digest('hex');
    return s.transaction(() => {
      if (!s.canEdit(session.principal))
        return s.problem(
          'FORBIDDEN',
          403,
          'Current permission does not allow this command.',
          command.requestId,
        );
      const prior = s.db
        .prepare(
          'SELECT * FROM requests WHERE principal = ? AND request_id = ?',
        )
        .get(session.principal, command.requestId);
      if (prior)
        return prior.fingerprint === fingerprint
          ? { status: prior.status, body: JSON.parse(prior.body) }
          : s.problem(
              'IDEMPOTENCY_MISMATCH',
              409,
              'The original request has different content or operation.',
              command.requestId,
            );
      const result = perform();
      const accepted = result.status === 202;
      const txId = transactionId ?? (accepted ? result.body.id : null);
      const row = txId ? this.row(session.principal, txId) : null;
      if (!accepted) result.body.requestId = command.requestId;
      const record = {
        requestId: command.requestId,
        nodeId: session.nodeId,
        operation,
        state: accepted ? 'accepted' : 'rejected',
        recordedAt: this.iso(),
        transactionId: row?.id ?? null,
        jobId: accepted
          ? operation === 'reconciliation'
            ? result.body.id
            : row.body.jobId
          : null,
        validationId: null,
        candidateRevision:
          row?.body.candidateRevision ??
          s.candidate(session.principal).revision,
        problem: accepted ? null : result.body,
      };
      s.db
        .prepare('INSERT INTO requests VALUES (?, ?, ?, ?, ?, ?, ?)')
        .run(
          session.principal,
          command.requestId,
          fingerprint,
          result.status,
          JSON.stringify(result.body),
          null,
          JSON.stringify(record),
        );
      return result;
    });
  }
  admissionProblem(principal, command, validation) {
    const s = this.store;
    const candidate = s.candidate(principal);
    if (this.blocked() || candidate.lockedByTransactionId)
      return s.problem(
        'TRANSACTION_ACTIVE',
        409,
        'An unresolved node transaction blocks admission.',
      );
    if (candidate.id !== command.candidateId)
      return s.problem(
        'NOT_FOUND',
        404,
        'Candidate does not belong to this workspace.',
      );
    if (candidate.revision !== command.expectedCandidateRevision)
      return s.problem(
        'ETAG_MISMATCH',
        412,
        'Candidate changed. Review the new version.',
      );
    if (
      candidate.currentGeneration !== command.expectedGeneration ||
      candidate.freshness !== 'current'
    )
      return s.problem(
        'CANDIDATE_STALE',
        409,
        'Review Drift / Stale or Conflict before applying.',
      );
    if (!s.meta('providerAvailable'))
      return s.problem(
        'PROVIDER_UNAVAILABLE',
        503,
        'Synthetic provider is unavailable.',
      );
    if (!validation || s.validations.expiration(principal, validation))
      return s.problem(
        'VALIDATION_EXPIRED',
        409,
        'Validate the current Candidate and safety policy again.',
      );
    if (
      validation.status !== 'passed' ||
      !validation.diff.length ||
      !same(validation.diff, candidate.intents) ||
      !validation.checks.length ||
      validation.checks.some((check) =>
        ['block', 'unknown'].includes(check.state),
      ) ||
      !validation.safetyPlan ||
      ['checkpoint', 'connectivityProbe', 'compareBeforeRollback'].some(
        (key) => validation.safetyPlan[key] !== 'available',
      ) ||
      s.meta('validationPolicy').safety !== 'available'
    )
      return s.problem(
        'VALIDATION_BLOCKED',
        409,
        'Checkpoint, probe and protected rollback must all be available.',
      );
    if (validation.safetyPlan.reauthenticationRequired)
      return s.problem(
        'REAUTH_REQUIRED',
        401,
        'Production reauthentication is not connected.',
      );
    return null;
  }
  start(session, command, key) {
    return this.command(
      session,
      'StartSafeApplyRequest',
      command,
      key,
      '/transactions',
      'safe-apply',
      null,
      () => {
        const s = this.store;
        const saved = s.db
          .prepare(
            'SELECT body FROM validations WHERE principal = ? AND id = ?',
          )
          .get(session.principal, command.validationId);
        const validation = saved ? JSON.parse(saved.body) : null;
        const problem = this.admissionProblem(
          session.principal,
          command,
          validation,
        );
        if (problem) return problem;
        const candidate = s.candidate(session.principal);
        const tx = {
          id: id('transaction'),
          nodeId: session.nodeId,
          requestId: command.requestId,
          candidateId: candidate.id,
          candidateRevision: candidate.revision,
          jobId: id('job'),
          correlationId: command.requestId,
          sequence: 1,
          serverTime: this.iso(),
          knowledge: 'current',
          phase: 'queued',
          outcome: 'unknown',
          safeApply: 'not-started',
          confirmationDeadline: null,
          locksCandidate: true,
          allowedActions: ['read', 'reconcile'],
          evidence: {
            databaseCommit: 'pending',
            daemonApply: 'pending',
            health: 'pending',
            checkpoint: 'pending',
            desiredGeneration: null,
            observedGeneration: candidate.currentGeneration,
            nextCfg: null,
            curCfg: null,
          },
        };
        candidate.lockedByTransactionId = tx.id;
        candidate.revision = id('revision');
        candidate.updatedAt = this.iso();
        const plan = {
          diff: validation.diff,
          validation,
          lockedRevision: candidate.revision,
          beforeGeneration: candidate.currentGeneration,
          reason: command.reason,
          checkpoint: null,
          commitGeneration: null,
          rollbackGeneration: null,
        };
        const job = {
          id: tx.jobId,
          nodeId: tx.nodeId,
          kind: 'safe-apply',
          state: 'queued',
          transactionId: tx.id,
          correlationId: tx.correlationId,
          updatedAt: this.iso(),
          message:
            'Safe Apply accepted; admission and request receipt are durable.',
        };
        s.db
          .prepare('INSERT INTO node_admission VALUES (?, ?)')
          .run(tx.nodeId, tx.id);
        s.db
          .prepare('INSERT INTO safe_transactions VALUES (?, ?, ?, ?, ?)')
          .run(
            tx.id,
            session.principal,
            JSON.stringify(tx),
            JSON.stringify(plan),
            JSON.stringify(job),
          );
        s.saveCandidate(session.principal, candidate);
        this.evidenceEntry(
          { id: tx.id, principal: session.principal, body: tx },
          'SAFE_APPLY_ACCEPTED',
          command.reason,
        );
        return { status: 202, body: tx };
      },
    );
  }
  preflightProblem(row) {
    const s = this.store;
    const candidate = s.candidate(row.principal);
    return (
      !s.canEdit(row.principal) ||
      s.meta('nodeBlocked') ||
      !s.meta('providerAvailable') ||
      s.meta('validationPolicy').safety !== 'available' ||
      s.meta('validationPolicy').revision !==
        row.plan.validation.policyRevision ||
      Date.parse(row.plan.validation.expiresAt) <= this.clock() ||
      candidate.id !== row.body.candidateId ||
      candidate.revision !== row.plan.lockedRevision ||
      candidate.lockedByTransactionId !== row.id ||
      candidate.currentGeneration !== row.plan.beforeGeneration ||
      !this.ownedMatches(row, 'base')
    );
  }
  release(row, clear) {
    const candidate = this.store.candidate(row.principal);
    if (candidate.lockedByTransactionId !== row.id)
      throw new Error('Candidate ownership was lost.');
    candidate.lockedByTransactionId = null;
    candidate.revision = id('revision');
    candidate.updatedAt = this.iso();
    if (clear) {
      candidate.intents = [];
      candidate.conflicts = [];
      candidate.conflictSnapshotId = null;
      candidate.baseGeneration = candidate.currentGeneration;
      candidate.freshness = 'current';
    }
    this.store.saveCandidate(row.principal, candidate);
    this.store.db
      .prepare(
        'DELETE FROM node_admission WHERE node_id = ? AND transaction_id = ?',
      )
      .run(row.body.nodeId, row.id);
    row.body.locksCandidate = false;
  }
  notStarted(row, message) {
    Object.assign(row.body, {
      phase: 'settled',
      safeApply: 'not-started',
      outcome: 'not-applied',
      knowledge: 'current',
    });
    row.body.evidence.databaseCommit = 'not-committed';
    row.job.state = 'failed';
    this.release(row, false);
    this.save(row, 'NOT_APPLIED', message);
  }
  writeImage(row, image) {
    const inventory = this.store.meta('inventory');
    inventory.generation = id('generation');
    inventory.snapshotId = id('snapshot');
    inventory.observedAt = this.iso();
    for (const port of inventory.items) {
      port.generation = inventory.generation;
      port.observedAt = inventory.observedAt;
      const change = row.plan.diff.find((item) => item.portId === port.id);
      if (change)
        port.configuration =
          image === 'base'
            ? row.plan.checkpoint[port.id]
            : {
                availability: 'known',
                value: change.mine,
                native: {
                  mode: change.mine.mode,
                  tag: change.mine.tag,
                  trunks: change.mine.trunks,
                },
              };
    }
    this.store.setMeta('inventory', inventory);
    return inventory.generation;
  }
  observe(row) {
    if (
      row.body.safeApply === 'rollback-conflict' ||
      !row.plan.commitGeneration
    )
      return;
    const tx = row.body;
    if (!this.store.meta('providerAvailable')) {
      if (tx.knowledge !== 'outcome-unknown') {
        tx.knowledge = 'outcome-unknown';
        tx.outcome = 'unknown';
        tx.evidence.daemonApply = 'unknown';
        tx.evidence.health = 'unknown';
        row.job.state = 'unknown';
        this.save(
          row,
          'OUTCOME_UNKNOWN',
          'Provider observations are unavailable. The transaction and deadline remain protected.',
        );
      }
    } else if (
      !this.ownedMatches(row) ||
      this.store.meta('inventory').generation !== row.plan.commitGeneration
    ) {
      if (tx.outcome !== 'needs-attention') {
        tx.knowledge = 'current';
        tx.outcome = 'needs-attention';
        tx.evidence.health = 'failed';
        tx.evidence.observedGeneration =
          this.store.meta('inventory').generation;
        row.job.state = 'unknown';
        this.save(
          row,
          'DRIFT_DETECTED',
          'Configuration changed after this transaction. Confirmation is blocked; rollback must compare its owned fields.',
        );
      }
    } else if (this.store.meta('validationPolicy').safety !== 'available') {
      if (tx.outcome !== 'degraded') {
        tx.knowledge = 'current';
        tx.outcome = 'degraded';
        tx.evidence.health = 'unknown';
        row.job.state = 'unknown';
        this.save(
          row,
          'SAFETY_DEGRADED',
          'Connectivity or protected rollback capabilities are unavailable. The server deadline remains active.',
        );
      }
    } else if (
      tx.phase === 'applying' ||
      tx.knowledge !== 'current' ||
      tx.outcome !== 'applied'
    ) {
      Object.assign(tx, {
        phase: 'awaiting-confirmation',
        safeApply: 'awaiting-confirmation',
        outcome: 'applied',
        knowledge: 'current',
      });
      Object.assign(tx.evidence, {
        daemonApply: 'applied',
        health: 'passed',
        observedGeneration: row.plan.commitGeneration,
      });
      row.job.state = 'running';
      this.save(
        row,
        'AWAITING_CONFIRMATION',
        'Synthetic inventory applied and local probe passed. Explicit confirmation is still required.',
      );
    }
  }
  requestRollback(row, code, reason, requestId) {
    Object.assign(row.body, {
      phase: 'rolling-back',
      safeApply: 'rollback-requested',
    });
    row.job.state = 'running';
    this.save(row, code, reason, requestId);
  }
  rollback(row) {
    if (
      !this.store.meta('providerAvailable') ||
      this.store.meta('validationPolicy').safety !== 'available'
    ) {
      if (
        row.body.knowledge !== 'outcome-unknown' ||
        row.body.outcome !== 'needs-attention'
      ) {
        row.body.knowledge = 'outcome-unknown';
        row.body.outcome = 'needs-attention';
        row.job.state = 'unknown';
        this.save(
          row,
          'ROLLBACK_WAITING',
          'Rollback cannot be verified while provider or safety capabilities are unavailable. Ownership remains locked.',
        );
      }
      return;
    }
    if (!row.plan.checkpoint || !this.ownedMatches(row)) {
      Object.assign(row.body, {
        phase: 'settled',
        safeApply: 'rollback-conflict',
        outcome: 'needs-attention',
        knowledge: 'current',
      });
      row.job.state = 'failed';
      this.save(
        row,
        'ROLLBACK_CONFLICT',
        'Owned VLAN fields no longer match this transaction. No external values were overwritten; manual recovery is required.',
      );
      return;
    }
    row.plan.rollbackGeneration = this.writeImage(row, 'base');
    Object.assign(row.body, {
      phase: 'settled',
      safeApply: 'rolled-back',
      outcome: 'not-applied',
      knowledge: 'current',
    });
    row.body.evidence.observedGeneration = row.plan.rollbackGeneration;
    row.job.state = 'succeeded';
    this.release(row, false);
    this.save(
      row,
      'ROLLED_BACK',
      'The checkpoint restored only this transaction’s VLAN fields. Candidate intent is retained for fresh review.',
    );
  }
  decide(session, transactionId, command, key) {
    return this.command(
      session,
      'DecisionRequest',
      command,
      key,
      `/transactions/${transactionId}/decisions`,
      'decision',
      transactionId,
      () => {
        const s = this.store;
        const row = this.row(session.principal, transactionId);
        if (!row)
          return s.problem(
            'NOT_FOUND',
            404,
            'Transaction not found in this workspace.',
          );
        const tx = row.body;
        if (
          command.decision === 'confirm' &&
          tx.confirmationDeadline &&
          Date.parse(tx.confirmationDeadline) <= this.clock()
        ) {
          if (
            tx.locksCandidate &&
            ['applying', 'awaiting-confirmation'].includes(tx.phase)
          )
            this.requestRollback(
              row,
              'CONFIRMATION_EXPIRED',
              'The server deadline won before confirmation.',
            );
          return s.problem(
            'DECISION_EXPIRED',
            409,
            'The server confirmation window has expired. Read the original transaction.',
          );
        }
        if (command.expectedTransactionSequence !== tx.sequence)
          return s.problem(
            'TRANSACTION_VERSION_CHANGED',
            409,
            'The transaction changed. Review its latest evidence.',
          );
        if (!this.wire(row).allowedActions.includes(command.decision))
          return s.problem(
            'OUTCOME_UNKNOWN',
            409,
            'Current evidence or permission does not allow this decision.',
          );
        if (command.decision === 'rollback')
          this.requestRollback(
            row,
            'ROLLBACK_REQUESTED',
            command.reason,
            command.requestId,
          );
        else {
          Object.assign(tx, {
            phase: 'settled',
            safeApply: 'confirmed',
            outcome: 'applied',
            knowledge: 'current',
          });
          row.job.state = 'succeeded';
          this.release(row, true);
          this.save(row, 'CONFIRMED', command.reason, command.requestId);
        }
        return { status: 202, body: this.wire(row) };
      },
    );
  }
  reconcile(session, transactionId, command, key) {
    return this.command(
      session,
      'ReconciliationRequest',
      command,
      key,
      `/transactions/${transactionId}/reconciliations`,
      'reconciliation',
      transactionId,
      () => {
        const row = this.row(session.principal, transactionId);
        if (!row)
          return this.store.problem(
            'NOT_FOUND',
            404,
            'Transaction not found in this workspace.',
          );
        const job = {
          id: id('job'),
          nodeId: row.body.nodeId,
          kind: 'reconciliation',
          state: 'queued',
          transactionId,
          correlationId: command.requestId,
          updatedAt: this.iso(),
          message:
            'Read-only reconciliation accepted for the original transaction.',
        };
        this.store.db
          .prepare('INSERT INTO reconciliation_jobs VALUES (?, ?, ?, ?)')
          .run(job.id, session.principal, transactionId, JSON.stringify(job));
        this.evidenceEntry(
          row,
          'RECONCILIATION_REQUESTED',
          job.message,
          command.requestId,
        );
        return { status: 202, body: job };
      },
    );
  }
  tick() {
    const s = this.store;
    s.transaction(() => {
      const rows = s.db
        .prepare(
          "SELECT id, principal FROM safe_transactions WHERE json_extract(body, '$.locksCandidate') = 1",
        )
        .all();
      for (const saved of rows) {
        const row = this.row(saved.principal, saved.id);
        const tx = row.body;
        if (!same(tx.allowedActions, this.wire(row).allowedActions))
          this.save(
            row,
            'ACTIONS_CHANGED',
            'Available actions updated from current permission and server evidence.',
          );
        if (tx.safeApply === 'rollback-conflict') continue;
        if (
          tx.confirmationDeadline &&
          Date.parse(tx.confirmationDeadline) <= this.clock() &&
          tx.phase !== 'rolling-back'
        ) {
          if (!row.plan.commitGeneration)
            this.notStarted(
              row,
              'The protection window expired before any synthetic write.',
            );
          else
            this.requestRollback(
              row,
              'CONFIRMATION_EXPIRED',
              'Server deadline expired; rollback does not depend on an open browser.',
            );
          continue;
        }
        if (tx.phase === 'queued') {
          tx.phase = 'preflight';
          row.job.state = 'running';
          this.save(
            row,
            'PREFLIGHT',
            'Rechecking admission before creating a checkpoint.',
          );
        } else if (tx.phase === 'preflight') {
          if (this.preflightProblem(row)) {
            this.notStarted(
              row,
              'Candidate, generation, permission or safety changed before execution.',
            );
            continue;
          }
          row.plan.checkpoint = Object.fromEntries(
            row.plan.diff.map((change) => [
              change.portId,
              s
                .meta('inventory')
                .items.find((port) => port.id === change.portId).configuration,
            ]),
          );
          tx.phase = 'committing';
          tx.safeApply = 'armed';
          tx.evidence.checkpoint = 'ready';
          tx.confirmationDeadline = new Date(
            this.clock() + this.windowSeconds * 1000,
          ).toISOString();
          this.save(
            row,
            'CHECKPOINT_READY',
            'Checkpoint, owned write set and server deadline persisted before application.',
          );
        } else if (tx.phase === 'committing') {
          if (this.preflightProblem(row)) {
            this.notStarted(
              row,
              'Final compare before write failed; nothing was applied.',
            );
            continue;
          }
          // Inventory and the commit receipt are committed together in this DB transaction.
          row.plan.commitGeneration = this.writeImage(row, 'mine');
          tx.phase = 'applying';
          tx.evidence.databaseCommit = 'committed';
          tx.evidence.desiredGeneration = row.plan.commitGeneration;
          this.save(
            row,
            'SYNTHETIC_COMMIT',
            'The synthetic VLAN write and its durable receipt committed atomically.',
          );
        } else if (tx.phase === 'rolling-back') this.rollback(row);
        else if (['applying', 'awaiting-confirmation'].includes(tx.phase))
          this.observe(row);
      }
      const jobs = s.db
        .prepare(
          "SELECT * FROM reconciliation_jobs WHERE json_extract(body, '$.state') = 'queued' ORDER BY rowid LIMIT 32",
        )
        .all();
      for (const saved of jobs) {
        const job = JSON.parse(saved.body);
        const row = this.row(saved.principal, saved.transaction_id);
        // Observation never calls the synthetic writer or releases an unresolved lock.
        if (['applying', 'awaiting-confirmation'].includes(row.body.phase))
          this.observe(row);
        job.state = 'succeeded';
        job.updatedAt = this.iso();
        job.message = `Observation completed: ${row.body.knowledge}, ${row.body.outcome}, ${row.body.safeApply}. Job completion is not confirmation.`;
        s.db
          .prepare('UPDATE reconciliation_jobs SET body = ? WHERE id = ?')
          .run(JSON.stringify(job), job.id);
        this.evidenceEntry(
          row,
          'RECONCILIATION_COMPLETED',
          job.message,
          job.correlationId,
        );
      }
    });
  }
}

export function startTransactionWorker(
  store,
  onError = (error) =>
    process.stderr.write(`Transaction worker: ${error.message}\n`),
) {
  const timer = setInterval(() => {
    try {
      store.transactions.tick();
    } catch (error) {
      onError(error);
    }
  }, 200);
  timer.unref();
  return () => clearInterval(timer);
}
