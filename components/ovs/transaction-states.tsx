'use client';

import { useState } from 'react';
import {
  CheckCircle2,
  Circle,
  Clock3,
  GitCompareArrows,
  ShieldCheck,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import { Notice, StatusBadge } from './foundation';
import {
  conflictCurrent,
  candidateName,
  candidateObject,
  remainingSeconds,
  type ApplyState,
  type Candidate,
  type ControlState,
  type ReconciliationResult,
} from '@/lib/change-control';
import { vlanLabel, nativeVlanFields, type Mode } from '@/lib/ovs-model';

export const outcomeLabels: Record<ApplyState, string> = {
  idle: 'No active transaction',
  countdown: 'Awaiting confirmation',
  confirmed: 'Confirmed',
  'rolled-back': 'Rolled back',
  'outcome-unknown': 'Outcome unknown',
  applied: 'Applied',
  'not-applied': 'Not applied',
  degraded: 'Partially applied / degraded',
  'needs-attention': 'Needs attention',
  'rollback-conflict': 'Rollback conflict',
};

export function CandidateChangeCard({
  candidate,
  mode,
  onRemove,
  locked,
}: {
  candidate: Candidate;
  mode: Mode;
  onRemove?: () => void;
  locked?: boolean;
}) {
  return (
    <article className="ovs-surface">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
        <div>
          <p className="font-mono text-sm font-semibold">
            {candidateObject(candidate)}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">
            {candidate.kind === 'vlan'
              ? 'VLAN configuration'
              : candidate.intent.title}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <StatusBadge tone="info">Modified</StatusBadge>
          {onRemove && (
            <Button variant="ghost" disabled={locked} onClick={onRemove}>
              Revert change
            </Button>
          )}
        </div>
      </div>
      <div className="grid gap-3 p-5 sm:grid-cols-[1fr_auto_1fr]">
        <div>
          <p className="text-xs text-muted-foreground">
            Base · generation {candidate.baseGeneration}
          </p>
          <p className="mt-2 whitespace-pre-wrap text-sm">
            {candidate.kind === 'vlan'
              ? vlanLabel(candidate.base)
              : candidate.intent.current}
          </p>
        </div>
        <GitCompareArrows
          aria-hidden="true"
          className="my-auto size-4 text-muted-foreground"
        />
        <div>
          <p className="text-xs text-muted-foreground">
            Your change · revision {candidate.revision}
          </p>
          <p className="mt-2 whitespace-pre-wrap text-sm font-medium text-primary">
            {candidate.kind === 'vlan'
              ? vlanLabel(candidate.mine)
              : candidate.intent.candidate}
          </p>
        </div>
      </div>
      {mode === 'expert' && (
        <details className="border-t p-5 text-sm">
          <summary className="cursor-pointer font-medium">
            {candidate.kind === 'vlan'
              ? 'OVS native mapping'
              : 'Native intent preview · prototype'}
          </summary>
          <pre className="mt-3 overflow-auto rounded border bg-muted p-3 font-mono text-xs">
            {candidate.kind === 'vlan'
              ? JSON.stringify(
                  {
                    Port: nativeVlanFields(candidate.mine),
                  },
                  null,
                  2,
                )
              : candidate.intent.candidate}
          </pre>
          <p className="mt-2 text-xs text-muted-foreground">
            Identity and opaque provider fields are retained. This is a mapping
            preview.
          </p>
        </details>
      )}
    </article>
  );
}

export function ConflictViewer({
  candidate,
  mode,
  onChoose,
  onCancel,
}: {
  candidate: Candidate;
  mode: Mode;
  onChoose: (choice: 'current' | 'mine') => void;
  onCancel: () => void;
}) {
  if (candidate.kind !== 'vlan')
    return (
      <Notice
        tone="danger"
        title="Fresh native snapshot required"
        urgent
        actions={
          <>
            <Button variant="outline" onClick={() => onChoose('current')}>
              Keep current system configuration
            </Button>
            <Button variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
          </>
        }
      >
        {candidateObject(candidate)} has a conflicting native change. The
        prototype has no authoritative three-way Bridge/Bond snapshot. Retaining
        your value requires a fresh backend snapshot; no force overwrite is
        available.
      </Notice>
    );
  return (
    <section
      aria-label="Three-way candidate conflict"
      className="ovs-surface p-5"
    >
      <Notice tone="danger" title="The same VLAN field changed" urgent>
        Choose which value to keep in the candidate for {candidate.port.name}.
        Both choices are reviewed before a new apply.
      </Notice>
      <div className="ovs-diff-grid">
        {[
          { name: 'Base', value: candidate.base, tone: 'neutral' },
          {
            name: 'Current system',
            value: conflictCurrent(candidate),
            tone: 'warning',
          },
          { name: 'Your change', value: candidate.mine, tone: 'info' },
        ].map((item) => (
          <div key={item.name} data-tone={item.tone}>
            <p className="text-xs font-semibold text-muted-foreground">
              {item.name}
            </p>
            <p className="mt-3 text-sm font-medium">{vlanLabel(item.value)}</p>
            {mode === 'expert' && (
              <p className="mt-3 font-mono text-xs">
                Port.vlan_mode / Port.tag / Port.trunks
              </p>
            )}
          </div>
        ))}
      </div>
      <div className="mt-5 flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => onChoose('current')}>
          Keep current system value
        </Button>
        <Button onClick={() => onChoose('mine')}>
          Use my value in candidate
        </Button>
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
      <p className="mt-3 text-sm text-muted-foreground">
        Choosing your value rebases the intent and invalidates earlier
        validation. It does not write to OVS.
      </p>
    </section>
  );
}

export function GenerationWarning({
  kind,
  base,
  current,
  onReview,
}: {
  kind: 'stale' | 'drift';
  base: number;
  current: number;
  onReview: () => void;
}) {
  return (
    <Notice
      tone="warning"
      title={
        kind === 'stale'
          ? 'Candidate base is stale'
          : 'Observed configuration has drifted'
      }
      actions={
        <Button variant="outline" onClick={onReview}>
          {kind === 'stale'
            ? 'Review & rebase non-overlapping change'
            : 'Reconcile observed state'}
        </Button>
      }
    >
      {kind === 'stale' ? (
        <>
          Base generation {base} · current generation {current}. The fixture
          changed MTU 1500 → 9000; the reviewed intent does not overlap. Rebase
          preserves that external change and requires fresh validation.
        </>
      ) : (
        <>
          Desired generation {current} · observed generation {base}. Inspect
          authoritative state before changing candidate intent. Reconciliation
          here is read-only.
        </>
      )}
    </Notice>
  );
}

export function OutcomeUnknownPanel({
  transaction,
  onReconcile,
  disconnected,
}: {
  transaction: ControlState['transaction'];
  onReconcile: (result: ReconciliationResult) => void;
  disconnected: boolean;
}) {
  const [result, setResult] = useState<ReconciliationResult>('applied');
  return (
    <section className="ovs-surface p-5">
      <Notice tone="uncertain" title="Apply status is uncertain" urgent>
        The acknowledgement is missing. The configuration may have been
        committed. Read the original transaction and observed state before any
        further change.
      </Notice>
      <p className="font-mono text-xs text-muted-foreground">
        {transaction.id} · {transaction.correlation}
      </p>
      <ol className="mt-5 space-y-3 text-sm">
        {[
          [
            'OVSDB connection',
            disconnected ? 'Unavailable' : 'Reachable in fixture',
          ],
          ['Configuration generation', 'Awaiting authoritative query'],
          ['Submitted intent', 'Awaiting comparison'],
          ['ovs-vswitchd applied state', 'Awaiting reconciliation'],
        ].map(([label, value], index) => (
          <li
            key={label}
            className="flex items-center justify-between gap-4 border-b pb-3"
          >
            <span className="flex items-center gap-2">
              {index === 0 && !disconnected ? (
                <CheckCircle2
                  aria-hidden="true"
                  className="size-4 text-[var(--ovs-success-fg)]"
                />
              ) : (
                <Circle
                  aria-hidden="true"
                  className="size-4 text-muted-foreground"
                />
              )}
              {label}
            </span>
            <span className="text-muted-foreground">{value}</span>
          </li>
        ))}
      </ol>
      <Button
        className="mt-5"
        onClick={() => onReconcile(result)}
        disabled={disconnected}
      >
        Read transaction & reconcile
      </Button>
      <details className="mt-5 border-t pt-3 text-sm">
        <summary className="cursor-pointer text-muted-foreground">
          Prototype fixture · reconciliation response
        </summary>
        <label className="mt-3 grid gap-2">
          Result returned by the simulated read
          <NativeSelect
            value={result}
            onChange={(event) =>
              setResult(event.target.value as ReconciliationResult)
            }
          >
            {(
              ['applied', 'not-applied', 'degraded', 'needs-attention'] as const
            ).map((value) => (
              <NativeSelectOption key={value} value={value}>
                {outcomeLabels[value]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
        <p className="mt-2 text-xs text-muted-foreground">
          The fixture selects a read response. It never retries the
          configuration transaction.
        </p>
      </details>
    </section>
  );
}

export function RollbackTimer({ seconds }: { seconds: number }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">Automatic rollback due in</p>
      <p
        role="timer"
        aria-label="Estimated rollback time remaining"
        className="ovs-timer"
      >
        {String(Math.floor(seconds / 60)).padStart(2, '0')}:
        {String(seconds % 60).padStart(2, '0')}
      </p>
    </div>
  );
}

export function TransactionBanner({
  state,
  now,
  onOpen,
}: {
  state: ControlState;
  now: number;
  onOpen: () => void;
}) {
  const status = state.transaction.status;
  if (
    ![
      'countdown',
      'outcome-unknown',
      'degraded',
      'needs-attention',
      'rollback-conflict',
    ].includes(status)
  )
    return null;
  return (
    <div
      className="ovs-transaction-bar"
      data-tone={
        status === 'countdown'
          ? 'info'
          : status === 'rollback-conflict'
            ? 'danger'
            : 'uncertain'
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <ShieldCheck aria-hidden="true" className="size-4" />
        <strong>{outcomeLabels[status]}</strong>
        <span>· {candidateName(state.transaction.snapshot)}</span>
        {status === 'countdown' && (
          <span className="inline-flex items-center gap-1 font-mono">
            <Clock3 aria-hidden="true" className="size-4" />
            {remainingSeconds(state.transaction, now)}s remaining
            {state.scenario === 'network-loss' && ' · disconnected estimate'}
          </span>
        )}
      </div>
      <Button variant="outline" size="sm" onClick={onOpen}>
        Open transaction
      </Button>
    </div>
  );
}
