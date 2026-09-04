import type { Port, VlanValue } from './ovs-model';
import type { ChangeIntent } from '../app/prototype-model';

export const scenarioLabels = {
  normal: 'Normal path',
  loading: 'Loading inventory',
  empty: 'Empty inventory',
  error: 'Inventory error',
  'permission-denied': 'Permission denied',
  'provider-unavailable': 'Provider unavailable',
  degraded: 'Provider degraded',
  conflict: 'Candidate conflict',
  stale: 'Stale candidate',
  drift: 'Configuration drift',
  'validation-blocked': 'Validation blocked',
  'outcome-unknown': 'Outcome unknown',
  'network-loss': 'Network loss',
  'rollback-conflict': 'Rollback conflict',
  'member-down': 'Bond member down',
  'lacp-mismatch': 'LACP mismatch',
  'provider-degraded': 'P1 provider degraded',
  'advanced-config': 'Advanced native configuration',
} as const;
export type Scenario = keyof typeof scenarioLabels;
export type ApplyState =
  | 'idle'
  | 'countdown'
  | 'confirmed'
  | 'rolled-back'
  | 'outcome-unknown'
  | 'applied'
  | 'not-applied'
  | 'degraded'
  | 'needs-attention'
  | 'rollback-conflict';
export type ReconciliationResult =
  | 'applied'
  | 'not-applied'
  | 'degraded'
  | 'needs-attention';
export type VlanCandidate = {
  kind: 'vlan';
  port: Port;
  base: VlanValue;
  mine: VlanValue;
  baseGeneration: number;
  revision: number;
};
export type TopologyCandidate = {
  kind: 'bridge' | 'bond';
  intent: ChangeIntent;
  baseGeneration: number;
  revision: number;
};
export type Candidate = VlanCandidate | TopologyCandidate;

export function candidateName(candidate: Candidate | null): string {
  return !candidate
    ? '—'
    : candidate.kind === 'vlan'
      ? candidate.port.name
      : candidate.intent.objectName;
}
export function candidateObject(candidate: Candidate | null): string {
  return !candidate
    ? 'Workspace/ws-183'
    : candidate.kind === 'vlan'
      ? `Port/${candidate.port.name}`
      : candidate.intent.evidenceObject;
}
export function candidateBridge(candidate: Candidate | null): string {
  return !candidate
    ? '—'
    : candidate.kind === 'vlan'
      ? candidate.port.bridge
      : (candidate.intent.bridgeName ??
        (candidate.kind === 'bridge'
          ? candidate.intent.objectName
          : 'See native intent'));
}
export type EvidenceEntry = {
  at: number;
  kind: 'Audit' | 'Event' | 'Job' | 'Health';
  text: string;
  object: string;
  correlation: string;
};
export type Transaction = {
  status: ApplyState;
  deadline: number | null;
  snapshot: Candidate | null;
  id: string;
  correlation: string;
};
export type ControlState = {
  candidate: Candidate | null;
  generation: number;
  validatedRevision: number | null;
  scenario: Scenario;
  transaction: Transaction;
  sequence: number;
  note: string;
  live: Record<string, VlanValue>;
  evidence: EvidenceEntry[];
  message: string;
  error: string | null;
};

export const initialControlState: ControlState = {
  candidate: null,
  generation: 1842,
  validatedRevision: null,
  scenario: 'normal',
  sequence: 0,
  transaction: {
    status: 'idle',
    deadline: null,
    snapshot: null,
    id: '',
    correlation: '',
  },
  note: '',
  live: {},
  evidence: [],
  message: 'Prototype ready · synthetic data',
  error: null,
};

export type ControlAction =
  | { type: 'scenario'; scenario: Scenario; now: number }
  | {
      type: 'stage-topology';
      intent: ChangeIntent;
      desktop: boolean;
      now: number;
    }
  | {
      type: 'record-evidence';
      kind: EvidenceEntry['kind'];
      text: string;
      object: string;
      correlation: string;
      now: number;
    }
  | {
      type: 'stage';
      port: Port;
      mine: VlanValue;
      desktop: boolean;
      now: number;
    }
  | { type: 'discard'; now: number }
  | { type: 'note'; note: string }
  | { type: 'validate'; now: number }
  | {
      type: 'rebase';
      choice: 'current' | 'mine' | 'non-overlapping';
      now: number;
    }
  | { type: 'inspect-drift'; now: number }
  | { type: 'start'; desktop: boolean; now: number }
  | { type: 'confirm' | 'rollback' | 'tick' | 'reconnect'; now: number }
  | { type: 'reconcile'; result: ReconciliationResult; now: number };

export function transactionLocked(status: ApplyState): boolean {
  return [
    'countdown',
    'outcome-unknown',
    'rollback-conflict',
    'degraded',
    'needs-attention',
  ].includes(status);
}

export function remainingSeconds(
  transaction: Transaction,
  now: number,
): number {
  return transaction.deadline === null
    ? 0
    : Math.max(0, Math.ceil((transaction.deadline - now) / 1000));
}

export function validateVlan(value: VlanValue): string | null {
  if (!['access', 'trunk', 'native-tagged'].includes(value.mode))
    return 'Choose a supported VLAN mode.';
  if (
    value.mode !== 'trunk' &&
    (!Number.isInteger(value.tag) || value.tag! < 1 || value.tag! > 4094)
  )
    return 'Access/native VLAN must be an integer from 1 to 4094.';
  if (value.mode === 'trunk' && value.tag !== null)
    return 'Trunk mode must not retain an access/native tag.';
  if (value.mode === 'access')
    return value.trunks ? 'Access mode must not retain trunk VLANs.' : null;
  const pieces = value.trunks.split(',').map((part) => part.trim());
  if (
    !value.trunks.trim() ||
    pieces.some((part) => !/^\d+(?:\s*-\s*\d+)?$/.test(part))
  )
    return 'Enter VLAN IDs or ranges, such as 120, 240-250.';
  if (
    pieces.some((part) => {
      const [start, end = start] = part.split('-').map(Number);
      return start < 1 || end > 4094 || start > end;
    })
  )
    return 'VLAN ranges must be ascending and within 1–4094.';
  return null;
}

export function sameVlan(a: VlanValue, b: VlanValue): boolean {
  const expand = (value: VlanValue) => {
    const ids = new Set<number>();
    for (const part of value.trunks.split(',').filter(Boolean)) {
      const [start, end = start] = part.trim().split('-').map(Number);
      if (end - start > 4094) continue;
      for (let id = start; id <= end; id++) ids.add(id);
    }
    return [...ids].sort((x, y) => x - y).join(',');
  };
  return a.mode === b.mode && a.tag === b.tag && expand(a) === expand(b);
}

export function conflictCurrent(candidate: VlanCandidate): VlanValue {
  return {
    mode: 'access',
    tag: candidate.base.tag === 130 ? 140 : 130,
    trunks: '',
  };
}

export function validationBlock(state: ControlState): string | null {
  if (transactionLocked(state.transaction.status))
    return 'Resolve the active transaction before preparing another apply.';
  if (!state.candidate) return 'The workspace is empty.';
  if (state.scenario !== 'normal')
    return `Resolve ${scenarioLabels[state.scenario].toLowerCase()} before validation.`;
  if (state.candidate.baseGeneration !== state.generation)
    return 'The candidate base generation is stale.';
  return state.candidate.kind === 'vlan'
    ? validateVlan(state.candidate.mine)
    : null;
}

export function applyBlock(state: ControlState): string | null {
  const blocked = validationBlock(state);
  if (blocked) return blocked;
  if (state.validatedRevision !== state.candidate?.revision)
    return 'Validate the current candidate revision first.';
  if (!state.note.trim()) return 'Add a change reason for Audit.';
  return null;
}

function reject(state: ControlState, error: string): ControlState {
  return { ...state, error, message: error };
}
function log(
  state: ControlState,
  now: number,
  kind: EvidenceEntry['kind'],
  text: string,
): ControlState {
  const candidate =
    state.transaction.snapshot && transactionLocked(state.transaction.status)
      ? state.transaction.snapshot
      : (state.candidate ?? state.transaction.snapshot);
  return {
    ...state,
    error: null,
    message: text,
    evidence: [
      ...state.evidence,
      {
        at: now,
        kind,
        text,
        object: candidateObject(candidate),
        correlation: state.transaction.id
          ? state.transaction.correlation
          : 'workspace/ws-183',
      },
    ],
  };
}

function rollback(state: ControlState, now: number): ControlState {
  const unsafe = ['rollback-conflict', 'conflict', 'stale', 'drift'].includes(
    state.scenario,
  );
  if (unsafe)
    return log(
      {
        ...state,
        validatedRevision: null,
        transaction: {
          ...state.transaction,
          status: 'rollback-conflict',
          deadline: null,
        },
      },
      now,
      'Job',
      'Rollback stopped: current fields no longer match this transaction. No external changes were overwritten.',
    );
  const generation = state.generation + 1;
  return log(
    {
      ...state,
      generation,
      validatedRevision: null,
      candidate: state.candidate
        ? { ...state.candidate, baseGeneration: generation }
        : null,
      transaction: {
        ...state.transaction,
        status: 'rolled-back',
        deadline: null,
      },
    },
    now,
    'Job',
    'Simulated compare-before-rollback passed. Previous configuration restored; candidate retained for review.',
  );
}

// This reducer is the shared prototype resource boundary for UI and WebMCP.
// Production must replace the fixtures with per-user Candidate and authoritative Job APIs.
export function transition(
  state: ControlState,
  action: ControlAction,
): ControlState {
  switch (action.type) {
    case 'record-evidence':
      return {
        ...state,
        evidence: [
          ...state.evidence,
          {
            at: action.now,
            kind: action.kind,
            text: action.text,
            object: action.object,
            correlation: action.correlation,
          },
        ],
      };
    case 'stage-topology': {
      if (!action.desktop)
        return reject(state, 'New configuration intent requires desktop.');
      if (transactionLocked(state.transaction.status))
        return reject(
          state,
          'An active transaction owns this workspace. Resolve it first.',
        );
      if (
        [
          'permission-denied',
          'provider-unavailable',
          'provider-degraded',
          'error',
          'network-loss',
        ].includes(state.scenario)
      )
        return reject(
          state,
          'Current authority or connectivity is unavailable.',
        );
      const intent = action.intent;
      if (
        !['bridge', 'bond'].includes(intent.kind) ||
        intent.objectType !== (intent.kind === 'bridge' ? 'Bridge' : 'Port') ||
        !/^[A-Za-z0-9_.-]{1,63}$/.test(intent.objectName) ||
        intent.evidenceObject !== `${intent.objectType}/${intent.objectName}` ||
        !intent.current.trim() ||
        !intent.candidate.trim() ||
        intent.current === intent.candidate
      )
        return reject(state, 'Invalid Bridge or Bond prototype intent.');
      if (
        intent.objectName === 'bond-provider' ||
        intent.objectName === 'br-offload' ||
        intent.bridgeName === 'br-offload'
      )
        return reject(state, 'This object is Observe-only.');
      if (
        state.candidate &&
        (state.candidate.kind !== intent.kind ||
          candidateName(state.candidate) !== intent.objectName)
      )
        return reject(
          state,
          'Review or discard the existing change before staging another object.',
        );
      const candidate: TopologyCandidate = {
        kind: intent.kind as 'bridge' | 'bond',
        intent: structuredClone(intent),
        baseGeneration: state.candidate?.baseGeneration ?? state.generation,
        revision: (state.candidate?.revision ?? 0) + 1,
      };
      return log(
        {
          ...state,
          candidate,
          validatedRevision: null,
          transaction: { ...initialControlState.transaction },
        },
        action.now,
        'Audit',
        `Staged ${intent.kind} intent for ${intent.objectName}. Running configuration is unchanged.`,
      );
    }
    case 'note':
      return { ...state, note: action.note, error: null };
    case 'scenario': {
      if (!(action.scenario in scenarioLabels))
        return reject(state, 'Unknown review scenario.');
      let transaction = state.transaction;
      if (
        action.scenario === 'outcome-unknown' &&
        transaction.status === 'countdown'
      )
        transaction = { ...transaction, status: 'outcome-unknown' };
      return {
        ...state,
        scenario: action.scenario,
        validatedRevision: null,
        transaction,
        message: `Review fixture: ${scenarioLabels[action.scenario]}`,
        error: null,
      };
    }
    case 'stage': {
      if (!action.desktop)
        return reject(state, 'New configuration intent requires desktop.');
      if (transactionLocked(state.transaction.status))
        return reject(
          state,
          'An active transaction owns this workspace. Resolve it first.',
        );
      if (
        [
          'permission-denied',
          'provider-unavailable',
          'error',
          'network-loss',
        ].includes(state.scenario)
      )
        return reject(
          state,
          'Current authority or connectivity is unavailable.',
        );
      if (action.port.scope === 'Observe' || action.port.authority !== 'OVS')
        return reject(state, 'This port is Observe-only.');
      const invalid = validateVlan(action.mine);
      if (invalid) return reject(state, invalid);
      if (
        state.candidate &&
        (state.candidate.kind !== 'vlan' ||
          state.candidate.port.name !== action.port.name)
      )
        return reject(
          state,
          'This P0 workspace supports one intent. Review or discard the existing change first.',
        );
      const base =
        (state.candidate?.kind === 'vlan' ? state.candidate.base : undefined) ??
        state.live[action.port.name] ??
        action.port.config;
      if (sameVlan(base, action.mine))
        return reject(state, 'No VLAN changes to stage.');
      const candidate: VlanCandidate = {
        kind: 'vlan',
        port: action.port,
        base,
        mine: action.mine,
        baseGeneration: state.candidate?.baseGeneration ?? state.generation,
        revision: (state.candidate?.revision ?? 0) + 1,
      };
      return log(
        {
          ...state,
          candidate,
          validatedRevision: null,
          transaction: { ...initialControlState.transaction },
        },
        action.now,
        'Audit',
        `Staged VLAN intent for ${action.port.name}. Running configuration is unchanged.`,
      );
    }
    case 'discard':
      if (transactionLocked(state.transaction.status))
        return reject(
          state,
          'Resolve the active transaction before discarding its candidate.',
        );
      return log(
        { ...state, candidate: null, validatedRevision: null },
        action.now,
        'Audit',
        'Candidate discarded. Running configuration is unchanged.',
      );
    case 'validate': {
      const blocked = validationBlock(state);
      if (blocked) return reject(state, blocked);
      return log(
        { ...state, validatedRevision: state.candidate!.revision },
        action.now,
        'Job',
        `Validation passed for revision ${state.candidate!.revision}, generation ${state.generation}.`,
      );
    }
    case 'rebase': {
      if (!state.candidate || transactionLocked(state.transaction.status))
        return reject(state, 'No editable candidate is available.');
      if (state.scenario !== 'stale' && state.scenario !== 'conflict')
        return reject(state, 'No candidate rebase is required.');
      if (state.scenario === 'conflict' && action.choice === 'non-overlapping')
        return reject(state, 'Resolve the overlapping VLAN field explicitly.');
      if (state.scenario === 'stale' && action.choice !== 'non-overlapping')
        return reject(state, 'Review the non-overlapping generation change.');
      const generation = state.generation + 1;
      if (state.candidate.kind !== 'vlan') {
        if (state.scenario === 'conflict' && action.choice !== 'current')
          return reject(
            state,
            'A fresh native Bridge/Bond snapshot is required. No force overwrite is available.',
          );
        const candidate =
          action.choice === 'current'
            ? null
            : {
                ...state.candidate,
                baseGeneration: generation,
                revision: state.candidate.revision + 1,
              };
        return log(
          {
            ...state,
            candidate,
            generation,
            scenario: 'normal',
            validatedRevision: null,
          },
          action.now,
          'Audit',
          candidate
            ? 'Non-overlapping topology intent rebased. Validate again before applying.'
            : 'Kept current native configuration; candidate intent removed.',
        );
      }
      const base =
        state.scenario === 'conflict'
          ? conflictCurrent(state.candidate)
          : state.candidate.base;
      const candidate =
        action.choice === 'current'
          ? null
          : {
              ...state.candidate,
              base,
              baseGeneration: generation,
              revision: state.candidate.revision + 1,
            };
      return log(
        {
          ...state,
          candidate,
          generation,
          scenario: 'normal',
          validatedRevision: null,
          live: { ...state.live, [state.candidate.port.name]: base },
        },
        action.now,
        'Audit',
        action.choice === 'current'
          ? 'Kept current system VLAN. Candidate change removed.'
          : 'Candidate rebased. Review and validate again before applying.',
      );
    }
    case 'inspect-drift':
      if (
        state.scenario !== 'drift' ||
        transactionLocked(state.transaction.status)
      )
        return reject(state, 'Inspect the active transaction evidence first.');
      return log(
        { ...state, scenario: 'normal', validatedRevision: null },
        action.now,
        'Job',
        'Read-only reconciliation fixture: observed generation now matches desired. No mutation was retried.',
      );
    case 'start': {
      const blocked = applyBlock(state);
      if (!action.desktop || blocked)
        return reject(
          state,
          !action.desktop ? 'Start Safe Apply from desktop.' : blocked!,
        );
      const sequence = state.sequence + 1;
      const transaction: Transaction = {
        status: 'countdown',
        deadline: action.now + 90_000,
        snapshot: state.candidate,
        id: `job-${2046 + sequence}`,
        correlation: `corr-demo-${sequence}`,
      };
      return log(
        { ...state, sequence, transaction },
        action.now,
        'Audit',
        `Simulated Safe Apply submitted. Reason: ${state.note.trim()}. Checkpoint, probe and compare-before-rollback fixture active; confirmation due in 90 seconds.`,
      );
    }
    case 'confirm': {
      if (state.transaction.status !== 'countdown')
        return reject(
          state,
          'No active provisional transaction can be confirmed.',
        );
      if (remainingSeconds(state.transaction, action.now) === 0)
        return transition(state, { type: 'tick', now: action.now });
      if (state.scenario !== 'normal')
        return reject(
          state,
          'Confirmation requires current connectivity and authoritative transaction evidence.',
        );
      const candidate = state.transaction.snapshot!;
      return log(
        {
          ...state,
          candidate: null,
          generation: state.generation + 1,
          validatedRevision: null,
          live:
            candidate.kind === 'vlan'
              ? { ...state.live, [candidate.port.name]: candidate.mine }
              : state.live,
          transaction: {
            ...state.transaction,
            status: 'confirmed',
            deadline: null,
          },
        },
        action.now,
        'Audit',
        'Safe Apply confirmed in the prototype. Candidate committed and workspace cleared.',
      );
    }
    case 'rollback':
      if (state.transaction.status !== 'countdown')
        return reject(
          state,
          'No active provisional transaction can be rolled back.',
        );
      if (state.scenario === 'network-loss')
        return reject(
          state,
          'Cannot send rollback while disconnected. Reconnect to read the authoritative result.',
        );
      return rollback(state, action.now);
    case 'tick':
      if (
        state.transaction.status !== 'countdown' ||
        remainingSeconds(state.transaction, action.now) > 0
      )
        return state;
      if (state.scenario === 'network-loss')
        return log(
          {
            ...state,
            transaction: { ...state.transaction, status: 'outcome-unknown' },
          },
          action.now,
          'Event',
          'Local countdown expired while disconnected. Rollback completion is unknown; read the transaction after reconnecting.',
        );
      return rollback(state, action.now);
    case 'reconnect':
      if (state.scenario !== 'network-loss')
        return reject(state, 'The connection is already available.');
      return transition(
        log(
          { ...state, scenario: 'normal' },
          action.now,
          'Event',
          'Prototype connection restored. Read the active transaction before taking another action.',
        ),
        { type: 'tick', now: action.now },
      );
    case 'reconcile': {
      if (state.scenario === 'network-loss')
        return reject(state, 'Reconnect before reading transaction evidence.');
      if (
        !['outcome-unknown', 'degraded', 'needs-attention'].includes(
          state.transaction.status,
        )
      )
        return reject(
          state,
          'No uncertain transaction is available to reconcile.',
        );
      if (
        !['applied', 'not-applied', 'degraded', 'needs-attention'].includes(
          action.result,
        )
      )
        return reject(state, 'Unknown reconciliation result.');
      const candidate = state.transaction.snapshot!;
      const applied = action.result === 'applied';
      return log(
        {
          ...state,
          scenario: 'normal',
          validatedRevision: null,
          candidate: applied ? null : state.candidate,
          generation: applied ? state.generation + 1 : state.generation,
          live:
            applied && candidate.kind === 'vlan'
              ? { ...state.live, [candidate.port.name]: candidate.mine }
              : state.live,
          transaction: {
            ...state.transaction,
            status: action.result,
            deadline: null,
          },
        },
        action.now,
        'Job',
        `Read-only reconciliation fixture: ${action.result}. No transaction was resubmitted.`,
      );
    }
  }
}
