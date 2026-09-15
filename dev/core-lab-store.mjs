import { DatabaseSync } from 'node:sqlite';
import { randomUUID, createHash } from 'node:crypto';
import { createContractValidator } from '../scripts/core-validator.mjs';
import { CoreLabValidation } from './core-lab-validation.mjs';
import { CoreLabTransactions } from './core-lab-transactions.mjs';
import {
  corePorts as prototypePorts,
  nativeVlanFields,
} from '../lib/ovs-model.ts';

const now = () => new Date().toISOString();
const id = (prefix) => `${prefix}-${randomUUID()}`;
const hash = (value) => createHash('sha256').update(value).digest('hex');
const canonical = (value) =>
  JSON.stringify(value, (key, item) =>
    key === 'trunks' && Array.isArray(item)
      ? [...item].sort((a, b) => a - b)
      : item && typeof item === 'object' && !Array.isArray(item)
        ? Object.fromEntries(
            Object.entries(item).sort(([a], [b]) => a.localeCompare(b)),
          )
        : item,
  );
const same = (a, b) => canonical(a) === canonical(b);
export const labUsers = {
  alice: 'Editor A',
  bob: 'Editor B',
  observer: 'Read-only reviewer',
};

export class CoreLabStore {
  constructor(path, options = {}) {
    this.db = new DatabaseSync(path);
    this.db.exec(`PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;
      CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS candidates (principal TEXT PRIMARY KEY, body TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS requests (principal TEXT NOT NULL, request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, status INTEGER NOT NULL, body TEXT NOT NULL, etag TEXT, record TEXT NOT NULL, PRIMARY KEY (principal, request_id));
      CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, principal TEXT NOT NULL, csrf TEXT NOT NULL, epoch TEXT NOT NULL, expires INTEGER NOT NULL);`);
    this.ajv = createContractValidator();
    if (!this.meta('inventory')) {
      const generation = id('generation');
      const nodeId = 'local-lab-node';
      const items = prototypePorts.map((port, index) => {
        const native = nativeVlanFields(port.config);
        const value = {
          mode: port.config.mode,
          tag: native.tag,
          trunks: native.trunks,
        };
        return {
          id: `port-${index + 1}`,
          nodeId,
          name: port.name,
          bridgeId: port.bridge,
          interfaces: (port.members ?? [port.interfaceName]).map((name, n) => ({
            id: `interface-${index + 1}-${n + 1}`,
            name,
          })),
          kind: port.members ? 'bond' : 'single',
          scope:
            port.scope === 'Manage'
              ? 'manage'
              : port.scope === 'Basic Manage'
                ? 'basic-manage'
                : 'observe',
          authority: port.authority === 'OVS' ? 'ovs' : 'external',
          provider: port.provider,
          configuration:
            port.scope === 'Observe'
              ? {
                  availability: 'unavailable',
                  value: null,
                  native: null,
                  reason: 'Synthetic provider-owned object.',
                }
              : {
                  availability: 'known',
                  value,
                  native: {
                    mode: native.vlan_mode,
                    tag: native.tag,
                    trunks: native.trunks,
                  },
                },
          linkState: port.state.toLowerCase(),
          linkReason:
            port.state === 'Unknown' ? 'Provider has no current sample.' : null,
          speedMbps:
            port.state !== 'Up' ? null : Number.parseFloat(port.speed) * 1000,
          observedAt: now(),
          generation,
          writableFields: port.scope === 'Observe' ? [] : ['vlan'],
        };
      });
      this.setMeta('inventory', {
        nodeId,
        generation,
        observedAt: now(),
        snapshotId: id('snapshot'),
        availability: 'complete',
        items,
        warnings: [],
        nextCursor: null,
      });
      this.setMeta('nodeBlocked', false);
      this.setMeta('providerAvailable', true);
    }
    this.validations = new CoreLabValidation(this, options);
    this.transactions = new CoreLabTransactions(this, options);
  }
  close() {
    this.db.close();
  }
  meta(key) {
    const row = this.db
      .prepare('SELECT value FROM metadata WHERE key = ?')
      .get(key);
    return row ? JSON.parse(row.value) : null;
  }
  setMeta(key, value) {
    // Invalidate old observations even if a capability is later restored.
    if (
      ['nodeBlocked', 'providerAvailable', 'revokedEditors'].includes(key) &&
      this.meta('validationPolicy') &&
      !same(this.meta(key), value)
    ) {
      const policy = this.meta('validationPolicy');
      this.setMeta('validationPolicy', { ...policy, revision: id('policy') });
    }
    this.db
      .prepare(
        'INSERT INTO metadata VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value',
      )
      .run(key, JSON.stringify(value));
  }
  transaction(fn) {
    this.db.exec('BEGIN IMMEDIATE');
    try {
      const result = fn();
      this.db.exec('COMMIT');
      return result;
    } catch (error) {
      this.db.exec('ROLLBACK');
      throw error;
    }
  }
  session(token) {
    if (!token) return null;
    const row = this.db
      .prepare('SELECT * FROM sessions WHERE token_hash = ? AND expires > ?')
      .get(hash(token), Date.now());
    if (!row) return null;
    return {
      principal: row.principal,
      csrfToken: row.csrf,
      epoch: row.epoch,
      nodeId: this.meta('inventory').nodeId,
      label: labUsers[row.principal],
      editable: this.canEdit(row.principal),
    };
  }
  login(principal, oldToken) {
    if (!Object.hasOwn(labUsers, principal))
      throw new Error('Unknown lab user.');
    return this.transaction(() => {
      this.logout(oldToken);
      const token = id('session');
      this.db
        .prepare('INSERT INTO sessions VALUES (?, ?, ?, ?, ?)')
        .run(
          hash(token),
          principal,
          id('csrf'),
          id('epoch'),
          Date.now() + 8 * 3600_000,
        );
      return { token, session: this.session(token) };
    });
  }
  logout(token) {
    if (token)
      this.db
        .prepare('DELETE FROM sessions WHERE token_hash = ?')
        .run(hash(token));
  }
  canEdit(principal) {
    return (
      Object.hasOwn(labUsers, principal) &&
      principal !== 'observer' &&
      !(this.meta('revokedEditors') ?? []).includes(principal)
    );
  }
  problem(code, status, detail, requestId = id('request')) {
    return {
      status,
      body: {
        type: `/api/problems/${code}`,
        title: code.replaceAll('_', ' '),
        status,
        detail,
        instance: `/api/v1/requests/${requestId}`,
        code,
        requestId,
        correlationId: id('correlation'),
        commandEffect: 'not-started',
        transactionId: null,
        candidateRevision: null,
      },
    };
  }
  saveCandidate(principal, value) {
    this.db
      .prepare(
        'INSERT INTO candidates VALUES (?, ?) ON CONFLICT(principal) DO UPDATE SET body = excluded.body',
      )
      .run(principal, JSON.stringify(value));
  }
  candidate(principal) {
    const inventory = this.meta('inventory');
    const row = this.db
      .prepare('SELECT body FROM candidates WHERE principal = ?')
      .get(principal);
    let candidate = row ? JSON.parse(row.body) : null;
    if (!candidate) {
      candidate = {
        id: id('candidate'),
        nodeId: inventory.nodeId,
        revision: id('revision'),
        baseGeneration: inventory.generation,
        currentGeneration: inventory.generation,
        freshness: 'current',
        intents: [],
        conflicts: [],
        conflictSnapshotId: null,
        lockedByTransactionId: null,
        updatedAt: now(),
      };
      this.saveCandidate(principal, candidate);
    }
    const owner = candidate.lockedByTransactionId
      ? this.transactions?.row(principal, candidate.lockedByTransactionId)
      : null;
    const ownGeneration = Boolean(
      owner?.plan.commitGeneration &&
      owner.plan.commitGeneration === inventory.generation,
    );
    const conflicts = candidate.intents.flatMap((intent) => {
      const current = inventory.items.find((port) => port.id === intent.portId)
        ?.configuration.value;
      return current &&
        !same(intent.base, current) &&
        !(ownGeneration && same(intent.mine, current))
        ? [
            {
              intentId: intent.id,
              portId: intent.portId,
              field: 'vlan',
              base: intent.base,
              current,
              mine: intent.mine,
            },
          ]
        : [];
    });
    candidate.currentGeneration = inventory.generation;
    candidate.freshness = conflicts.length
      ? 'conflict'
      : candidate.intents.length &&
          candidate.baseGeneration !== inventory.generation &&
          !ownGeneration
        ? 'stale'
        : 'current';
    candidate.conflicts = conflicts;
    candidate.conflictSnapshotId = conflicts.length
      ? `conflict-${hash(canonical([inventory.generation, conflicts])).slice(0, 32)}`
      : null;
    return candidate;
  }
  snapshot(principal) {
    const candidate = this.candidate(principal);
    return { candidate, etag: `"${hash(canonical(candidate))}"` };
  }
  workspace(session) {
    const latestValidation = this.validations.latest(session.principal);
    return {
      nodeId: session.nodeId,
      serverTime: new Date(this.validations.clock()).toISOString(),
      candidate: this.candidate(session.principal),
      latestValidation,
      activeTransactions: this.transactions.active(session.principal),
      latestTransaction: this.transactions.latest(session.principal),
      pendingRequests: [],
      nodeWriteBlocked: this.transactions.blocked(),
      permissions: {
        editCandidate: this.canEdit(session.principal),
        validate: this.canEdit(session.principal),
        startSafeApply:
          this.canEdit(session.principal) &&
          !this.transactions.blocked() &&
          this.meta('providerAvailable') &&
          this.meta('validationPolicy').safety === 'available' &&
          latestValidation?.status === 'passed',
      },
    };
  }
  inventory(cursor, search = '') {
    if (!this.meta('providerAvailable'))
      return this.problem(
        'PROVIDER_UNAVAILABLE',
        503,
        'Synthetic inventory provider is unavailable.',
      );
    const data = this.meta('inventory');
    const items = data.items.filter((port) =>
      `${port.name} ${port.bridgeId} ${port.interfaces.map((item) => item.name).join(' ')}`
        .toLowerCase()
        .includes(search.toLowerCase()),
    );
    let offset = 0;
    if (cursor) {
      const [snapshotId, index, queryHash] = cursor.split(':');
      if (
        snapshotId !== data.snapshotId ||
        !/^\d+$/.test(index) ||
        queryHash !== hash(search).slice(0, 12) ||
        Number(index) > items.length
      )
        return this.problem(
          'CANDIDATE_STALE',
          409,
          'Inventory snapshot expired. Refresh from the first page.',
        );
      offset = Number(index);
    }
    const pageSize = 3;
    return {
      status: 200,
      body: {
        ...data,
        items: items.slice(offset, offset + pageSize),
        nextCursor:
          offset + pageSize < items.length
            ? `${data.snapshotId}:${offset + pageSize}:${hash(search).slice(0, 12)}`
            : null,
      },
    };
  }
  readRequest(principal, requestId) {
    const row = this.db
      .prepare(
        'SELECT record FROM requests WHERE principal = ? AND request_id = ?',
      )
      .get(principal, requestId);
    return row
      ? { status: 200, body: JSON.parse(row.record) }
      : this.problem(
          'NOT_FOUND',
          404,
          'No request evidence is available for this user.',
          requestId,
        );
  }
  mutate(session, command, etag, key) {
    if (!this.ajv.getSchema('#/components/schemas/CandidateMutation')(command))
      return this.problem(
        'INVALID_INTENT',
        422,
        'Use a typed Candidate command.',
      );
    const requestId = command.requestId;
    if (!this.canEdit(session.principal))
      return this.problem(
        'FORBIDDEN',
        403,
        'This lab role is read-only.',
        requestId,
      );
    if (key !== requestId)
      return this.problem(
        'IDEMPOTENCY_MISMATCH',
        409,
        'Idempotency-Key must match requestId.',
        requestId,
      );
    const fingerprint = hash(
      canonical({
        node: session.nodeId,
        method: 'PATCH',
        path: '/candidate',
        etag,
        command,
      }),
    );
    return this.transaction(() => {
      const prior = this.db
        .prepare(
          'SELECT * FROM requests WHERE principal = ? AND request_id = ?',
        )
        .get(session.principal, requestId);
      if (prior)
        return prior.fingerprint === fingerprint
          ? {
              status: prior.status,
              body: JSON.parse(prior.body),
              etag: prior.etag,
            }
          : this.problem(
              'IDEMPOTENCY_MISMATCH',
              409,
              'The original request has different content.',
              requestId,
            );
      const reviewed = this.snapshot(session.principal);
      const candidate = reviewed.candidate;
      const reject = (code, status, detail) =>
        this.problem(code, status, detail, requestId);
      let result;
      if (!etag)
        result = reject(
          'PRECONDITION_REQUIRED',
          428,
          'Read Candidate and supply its ETag.',
        );
      else if (etag !== reviewed.etag)
        result = reject(
          'ETAG_MISMATCH',
          412,
          'Candidate changed. Read and review the latest version.',
        );
      else if (candidate.lockedByTransactionId || this.transactions.blocked())
        result = reject(
          'TRANSACTION_ACTIVE',
          409,
          'An unresolved node operation blocks new intent.',
        );
      else if (
        command.operation !== 'discard' &&
        !this.meta('providerAvailable')
      )
        result = reject(
          'PROVIDER_UNAVAILABLE',
          503,
          'Current native observations are unavailable.',
        );
      else if (command.operation === 'discard') candidate.intents = [];
      else if (command.operation === 'set-vlan') {
        const port = this.meta('inventory').items.find(
          (item) => item.id === command.portId,
        );
        if (!port) result = reject('NOT_FOUND', 404, 'Port was not found.');
        else if (
          port.authority !== 'ovs' ||
          !port.writableFields.includes('vlan') ||
          port.configuration.availability !== 'known'
        )
          result = reject(
            'UNSUPPORTED_CONFIGURATION',
            422,
            'This Port is Observe-only or its configuration is unavailable.',
          );
        else if (
          candidate.freshness !== 'current' ||
          command.expectedGeneration !== candidate.currentGeneration
        )
          result = reject(
            'CANDIDATE_STALE',
            409,
            'Review generation and conflicts first.',
          );
        else if (
          candidate.intents.length &&
          candidate.intents[0].portId !== command.portId
        )
          result = reject(
            'INVALID_INTENT',
            422,
            'Review or discard the existing object first.',
          );
        else {
          const base = candidate.intents[0]?.base ?? port.configuration.value;
          candidate.intents = same(base, command.mine)
            ? []
            : [
                {
                  id: candidate.intents[0]?.id ?? id('intent'),
                  portId: port.id,
                  base,
                  mine: command.mine,
                },
              ];
        }
      } else {
        if (
          command.currentGeneration !== candidate.currentGeneration ||
          command.conflictSnapshotId !== candidate.conflictSnapshotId
        )
          result = reject(
            'CANDIDATE_STALE',
            409,
            'The reviewed conflict snapshot changed.',
          );
        else if (
          candidate.conflicts.length !== command.resolutions.length ||
          command.resolutions.some(
            (choice) =>
              !candidate.conflicts.some(
                (conflict) => conflict.intentId === choice.intentId,
              ),
          )
        )
          result = reject(
            'FIELD_CONFLICT',
            409,
            'Choose one resolution for every overlapping intent.',
          );
        else
          candidate.intents = candidate.intents.flatMap((intent) => {
            const conflict = candidate.conflicts.find(
              (item) => item.intentId === intent.id,
            );
            if (!conflict) return [intent];
            return command.resolutions.find(
              (item) => item.intentId === intent.id,
            )?.choice === 'current' || same(conflict.current, intent.mine)
              ? []
              : [{ ...intent, base: conflict.current }];
          });
      }
      if (!result) {
        candidate.revision = id('revision');
        candidate.baseGeneration = candidate.currentGeneration;
        candidate.freshness = 'current';
        candidate.conflicts = [];
        candidate.conflictSnapshotId = null;
        candidate.updatedAt = now();
        this.saveCandidate(session.principal, candidate);
        const next = this.snapshot(session.principal);
        result = { status: 200, body: next.candidate, etag: next.etag };
      }
      const record = {
        requestId,
        nodeId: session.nodeId,
        operation: 'candidate',
        state: result.status === 200 ? 'accepted' : 'rejected',
        recordedAt: now(),
        transactionId: null,
        jobId: null,
        validationId: null,
        candidateRevision:
          result.status === 200 ? result.body.revision : candidate.revision,
        problem: result.status === 200 ? null : result.body,
      };
      this.db
        .prepare('INSERT INTO requests VALUES (?, ?, ?, ?, ?, ?, ?)')
        .run(
          session.principal,
          requestId,
          fingerprint,
          result.status,
          JSON.stringify(result.body),
          result.etag ?? null,
          JSON.stringify(record),
        );
      return result;
    });
  }
  // Test harness only: represent external OVS observations; never called by Candidate writes.
  externalChange(portId, value) {
    return this.transaction(() => {
      const inventory = this.meta('inventory');
      inventory.generation = id('generation');
      inventory.snapshotId = id('snapshot');
      inventory.observedAt = now();
      for (const port of inventory.items) {
        port.generation = inventory.generation;
        port.observedAt = inventory.observedAt;
        if (port.id === portId)
          port.configuration = {
            availability: 'known',
            value,
            native: { ...value },
          };
      }
      this.setMeta('inventory', inventory);
    });
  }
}
