'use client';
import {
  nativeConfirmBlock,
  nativeRollbackBlock,
} from '@/lib/native-capability';

import { useState } from 'react';
import {
  ArrowLeft,
  CheckCircle2,
  FileClock,
  Layers3,
  ShieldCheck,
  Undo2,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Notice, PageHeader, StatusBadge } from './foundation';
import {
  CandidateChangeCard,
  ConflictViewer,
  GenerationWarning,
  OutcomeUnknownPanel,
  RollbackTimer,
  outcomeLabels,
} from './transaction-states';
import {
  applyBlock,
  candidateName,
  candidateObject,
  candidateBridge,
  validationBlock,
  remainingSeconds,
  transactionLocked,
  type ControlAction,
  type ControlState,
  type Scenario,
} from '@/lib/change-control';
import type { Mode, View } from '@/lib/ovs-model';

type Props = {
  state: ControlState;
  mode: Mode;
  go: (view: View) => void;
  act: (action: ControlAction) => ControlState;
};
const now = () => Date.now();
const desktop = () => window.matchMedia('(min-width: 1024px)').matches;

export function ScenarioBanner({ state, act, go }: Omit<Props, 'mode'>) {
  const scenario = state.scenario;
  if (
    scenario === 'normal' ||
    [
      'loading',
      'empty',
      'error',
      'permission-denied',
      'provider-unavailable',
      'degraded',
    ].includes(scenario)
  )
    return null;
  const messages: Partial<Record<Scenario, [string, string]>> = {
    conflict: [
      'Candidate conflict',
      'An external change overlaps a candidate field. Review the conflict and available native evidence in Changes.',
    ],
    stale: [
      'Candidate needs a newer base',
      'An external MTU change advanced the generation. Review the non-overlapping merge in Changes.',
    ],
    drift: [
      'Desired and observed state differ',
      'Reconcile authoritative observations before preparing an apply.',
    ],
    'validation-blocked': [
      'Policy validation blocked',
      'This fixture represents a management-path policy failure. Expert Mode cannot bypass it.',
    ],
    'network-loss': [
      'Management connection unavailable',
      'The displayed countdown is an estimate. Confirm and rollback requests need a restored connection.',
    ],
    'outcome-unknown': [
      'Missing completion evidence',
      'An existing transaction must be reconciled. To review this fixture, first start Safe Apply in Normal path.',
    ],
    'rollback-conflict': [
      'Compare-before-rollback fixture',
      'An external writer changed a touched field. Rollback must stop without overwriting that value.',
    ],
    'member-down': [
      'Bond member degraded',
      'Read member health and repair redundancy before validating a new configuration.',
    ],
    'lacp-mismatch': [
      'LACP policy mismatch',
      'Review peer and local LACP settings. Expert mode does not bypass validation.',
    ],
    'provider-degraded': [
      'Partial provider data',
      'Known inventory remains available; missing native state blocks new configuration.',
    ],
    'advanced-config': [
      'Advanced native configuration',
      'Review preserved native fields in Expert mode. Return to a current, healthy observation before validation.',
    ],
  };
  const item = messages[scenario];
  if (!item) return null;
  return (
    <Notice
      tone={
        scenario === 'outcome-unknown'
          ? 'uncertain'
          : scenario === 'conflict' || scenario === 'validation-blocked'
            ? 'danger'
            : 'warning'
      }
      title={item[0]}
      actions={
        scenario === 'network-loss' ? (
          <Button
            variant="outline"
            onClick={() => act({ type: 'reconnect', now: now() })}
          >
            Reconnect & read status
          </Button>
        ) : ['conflict', 'stale', 'drift'].includes(scenario) ? (
          <Button variant="outline" onClick={() => go('workspace')}>
            Review in Changes
          </Button>
        ) : undefined
      }
    >
      {item[1]}
    </Notice>
  );
}

export function Workspace({ state, mode, act, go }: Props) {
  const candidate = state.candidate;
  const locked = transactionLocked(state.transaction.status);
  return (
    <>
      <PageHeader
        id="P0-05"
        eyebrow="Global / Changes"
        title="Candidate workspace"
        description="Review pending intent before validation. Running configuration changes only through Apply."
        actions={
          <Button variant="outline" onClick={() => go('ports')}>
            Browse ports
          </Button>
        }
      />
      {!candidate ? (
        <div className="ovs-surface mt-6 px-6 py-14 text-center">
          <Layers3
            aria-hidden="true"
            className="mx-auto size-7 text-muted-foreground"
          />
          <h2 className="mt-4 font-semibold">Workspace is clean</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            Open a managed port to prepare a change.
          </p>
          <Button className="mt-5" onClick={() => go('ports')}>
            Browse managed ports
          </Button>
        </div>
      ) : (
        <>
          <div className="my-5 flex flex-wrap items-center justify-between gap-3 text-sm">
            <span>
              1 pending change · workspace{' '}
              <span className="font-mono">ws-183</span>
            </span>
            <span className="text-muted-foreground">
              Base generation {candidate.baseGeneration} · operator.demo
            </span>
          </div>
          <CandidateChangeCard
            candidate={candidate}
            mode={mode}
            locked={locked}
            onRemove={() => act({ type: 'discard', now: now() })}
          />
          {state.scenario === 'conflict' && !locked && (
            <div className="mt-4">
              <ConflictViewer
                candidate={candidate}
                mode={mode}
                onChoose={(choice) =>
                  act({ type: 'rebase', choice, now: now() })
                }
                onCancel={() => go('ports')}
              />
            </div>
          )}
          {state.scenario === 'stale' && !locked && (
            <GenerationWarning
              kind="stale"
              base={candidate.baseGeneration}
              current={state.generation + 1}
              onReview={() =>
                act({ type: 'rebase', choice: 'non-overlapping', now: now() })
              }
            />
          )}
          {state.scenario === 'drift' && !locked && (
            <GenerationWarning
              kind="drift"
              base={state.generation - 1}
              current={state.generation}
              onReview={() => act({ type: 'inspect-drift', now: now() })}
            />
          )}
          {locked && (
            <Notice
              tone="uncertain"
              title="This candidate belongs to an active transaction"
              actions={
                <Button variant="outline" onClick={() => go('safe-apply')}>
                  Open transaction
                </Button>
              }
            >
              Editing and resubmission are unavailable until the original result
              is known.
            </Notice>
          )}
          <div className="mt-5 flex flex-wrap items-center justify-between gap-3 border-t pt-4">
            <StatusBadge
              tone={
                state.validatedRevision === candidate.revision
                  ? 'success'
                  : 'warning'
              }
            >
              {state.validatedRevision === candidate.revision
                ? 'Validated revision'
                : 'Validation required'}
            </StatusBadge>
            <Button onClick={() => go('diff')}>Review diff & validation</Button>
          </div>
        </>
      )}
    </>
  );
}

export function DiffPage({ state, mode, act, go }: Props) {
  const [reviewOpen, setReviewOpen] = useState(false);
  const candidate = state.candidate;
  const blocked = applyBlock(state);
  const validateBlocked = validationBlock(state);
  const validated =
    candidate &&
    state.validatedRevision === candidate.revision &&
    !validateBlocked;
  return (
    <>
      <PageHeader
        id="P0-06"
        eyebrow="Changes / Diff & validation"
        title="Review candidate"
        description="Verify the exact intent, validation results and safety mechanism before applying."
        actions={
          <Button variant="outline" onClick={() => go('workspace')}>
            <ArrowLeft /> Changes
          </Button>
        }
      />
      {!candidate ? (
        <Notice
          title="There is no candidate to validate"
          actions={
            <Button variant="outline" onClick={() => go('ports')}>
              Browse ports
            </Button>
          }
        >
          Prepare a change from a managed port.
        </Notice>
      ) : (
        <div className="mt-6 grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
          <div className="space-y-4">
            <CandidateChangeCard candidate={candidate} mode={mode} />
            <section className="ovs-surface p-5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="font-semibold">Validation results</h2>
                <Button
                  variant="outline"
                  disabled={Boolean(validateBlocked)}
                  onClick={() => act({ type: 'validate', now: now() })}
                >
                  Validate candidate
                </Button>
              </div>
              <ul className="mt-4 divide-y">
                {[
                  candidate.kind === 'vlan'
                    ? 'VLAN schema and ranges'
                    : candidate.kind === 'bond'
                      ? 'Bond intent and Interface membership'
                      : candidate.kind === 'isolation'
                        ? 'Native Port.protected policy and four safety gates'
                        : 'Bridge configuration intent',
                  'Capability and OVS authority',
                  'Permission and management-path policy',
                  `Generation ${candidate.baseGeneration} · candidate revision ${candidate.revision}`,
                ].map((check) => (
                  <li
                    key={check}
                    className="flex items-center justify-between gap-3 py-3 text-sm"
                  >
                    <span>{check}</span>
                    <StatusBadge
                      tone={
                        validated
                          ? 'success'
                          : validateBlocked
                            ? 'warning'
                            : 'neutral'
                      }
                    >
                      {validated
                        ? 'Pass'
                        : validateBlocked
                          ? 'Blocked'
                          : 'Not checked'}
                    </StatusBadge>
                  </li>
                ))}
              </ul>
              {validateBlocked && (
                <Notice tone="warning" title="Validation unavailable">
                  {validateBlocked}
                </Notice>
              )}
            </section>
          </div>
          <aside className="space-y-4">
            <section className="ovs-surface p-5">
              <h2 className="font-semibold">Apply contract</h2>
              <dl className="mt-4 space-y-3 text-sm">
                {[
                  ['Strategy', 'Safe Apply'],
                  ['Confirmation window', '90 seconds'],
                  ['Fallback', 'Compare before rollback'],
                  ['Affected object', candidateObject(candidate)],
                  ['Bridge', candidateBridge(candidate)],
                ].map(([label, value]) => (
                  <div key={label} className="flex justify-between gap-3">
                    <dt className="text-muted-foreground">{label}</dt>
                    <dd className="text-right font-medium">{value}</dd>
                  </div>
                ))}
              </dl>
            </section>
            <section className="ovs-surface p-5">
              <label htmlFor="change-reason" className="text-sm font-medium">
                Change reason{' '}
                <span className="text-muted-foreground">(required)</span>
              </label>
              <Input
                id="change-reason"
                className="mt-2"
                value={state.note}
                onChange={(event) =>
                  act({ type: 'note', note: event.target.value })
                }
                placeholder="Why is this change needed?"
              />
              <p className="mt-3 text-xs text-muted-foreground">
                Recorded with the actor, object, diff and correlation ID.
              </p>
            </section>
            {blocked && (
              <Notice tone="warning" title="Apply is not ready">
                {blocked}
              </Notice>
            )}
            <Button
              disabled={Boolean(blocked)}
              onClick={() => setReviewOpen(true)}
              className="hidden w-full lg:inline-flex"
            >
              <ShieldCheck /> Review & apply
            </Button>
            <p className="text-sm text-muted-foreground lg:hidden">
              Starting a new Safe Apply requires desktop. Existing transactions
              can be handled here.
            </p>
          </aside>
        </div>
      )}
      <Dialog open={reviewOpen} onOpenChange={setReviewOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>Apply with connectivity protection</DialogTitle>
            <DialogDescription>
              Review the affected path and recovery window. Changes become
              provisional until you confirm.
            </DialogDescription>
          </DialogHeader>
          <Notice
            tone="warning"
            title={
              candidateName(candidate) === 'mgmt0' ||
              candidateBridge(candidate) === 'br-mgmt'
                ? 'Management path change'
                : 'Connectivity may be affected'
            }
          >
            {candidateName(candidate)} · {candidateBridge(candidate)}
          </Notice>
          <ul className="space-y-3 text-sm">
            {[
              'Pre-change checkpoint available',
              'Management connectivity probe configured',
              'Compare-before-rollback protection enabled',
            ].map((label) => (
              <li key={label} className="flex gap-2">
                <CheckCircle2
                  aria-hidden="true"
                  className="size-4 text-[var(--ovs-success-fg)]"
                />
                {label}
              </li>
            ))}
          </ul>
          <p className="border-t pt-3 text-sm">
            Confirmation window: <strong>90 seconds</strong>. Safety resources
            are simulated in this prototype.
          </p>
          {blocked && (
            <Notice tone="danger" title="Readiness changed">
              {blocked}
            </Notice>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setReviewOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={Boolean(blocked)}
              onClick={() => {
                const next = act({
                  type: 'start',
                  desktop: desktop(),
                  now: now(),
                });
                if (!next.error) {
                  setReviewOpen(false);
                  go('safe-apply');
                }
              }}
            >
              Apply safely
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function SafeApply({
  state,
  act,
  go,
  currentTime,
}: Props & { currentTime: number }) {
  const transaction = state.transaction;
  const status = transaction.status;
  const disconnected = state.scenario === 'network-loss';
  const seconds = remainingSeconds(transaction, currentTime);
  const uncertain = ['outcome-unknown', 'degraded', 'needs-attention'].includes(
    status,
  );
  const nativeConfirm =
    transaction.snapshot?.kind === 'isolation'
      ? nativeConfirmBlock(
          state.nativeCapability,
          state.scenario,
          state.generation,
          currentTime,
        )
      : null;
  const nativeRollback =
    transaction.snapshot?.kind === 'isolation'
      ? nativeRollbackBlock(
          state.nativeCapability,
          state.scenario,
          state.generation,
          currentTime,
        )
      : null;
  return (
    <>
      <PageHeader
        id="P0-07"
        eyebrow="Operations / Safe Apply"
        title={outcomeLabels[status]}
        description={
          status === 'countdown'
            ? 'Verify management connectivity and expected traffic before confirming.'
            : 'Inspect the original transaction and its authoritative outcome.'
        }
        actions={
          <Button variant="outline" onClick={() => go('evidence')}>
            <FileClock /> Evidence
          </Button>
        }
      />
      {status === 'idle' ? (
        <Notice
          title="No provisional change is active"
          actions={
            <Button variant="outline" onClick={() => go('workspace')}>
              Review candidate
            </Button>
          }
        >
          Start from Changes, validate the current revision, and review Safe
          Apply. Confirmation and rollback become available only for an active
          transaction.
        </Notice>
      ) : (
        <>
          <div className="my-5 flex flex-wrap items-center gap-3 text-sm">
            <span className="font-mono">{transaction.id}</span>
            <span>{candidateObject(transaction.snapshot)}</span>
            <span className="text-muted-foreground">
              {transaction.correlation}
            </span>
          </div>
          {status === 'countdown' && (
            <section className="ovs-surface p-5">
              <div className="flex flex-wrap items-start justify-between gap-5">
                <div>
                  <StatusBadge tone="info">
                    Provisional configuration
                  </StatusBadge>
                  <h2 className="mt-4 text-xl font-semibold">
                    Confirm that connectivity is working
                  </h2>
                  <p className="mt-2 text-sm text-muted-foreground">
                    Check the management session and traffic on{' '}
                    {candidateName(transaction.snapshot)}.
                  </p>
                </div>
                <RollbackTimer seconds={seconds} />
              </div>
              <div className="mt-5 h-1.5 overflow-hidden rounded bg-muted">
                <div
                  className="h-full bg-primary"
                  style={{ width: `${Math.min(100, (seconds / 90) * 100)}%` }}
                />
              </div>
              <div className="ovs-stepper mt-5 text-sm">
                {[
                  'Committed',
                  'Applied',
                  'Health checked',
                  'Awaiting confirmation',
                ].map((label, index) => (
                  <span key={label}>
                    <StatusBadge
                      tone={
                        disconnected
                          ? 'uncertain'
                          : index < 3
                            ? 'success'
                            : 'info'
                      }
                    >
                      {disconnected ? `${label} · unverified` : label}
                    </StatusBadge>
                  </span>
                ))}
              </div>
              <p className="mt-4 text-sm text-muted-foreground">
                {disconnected
                  ? 'Timer is a local estimate. The server result must be read after reconnecting.'
                  : 'Checkpoint and health results are synthetic fixture responses.'}
              </p>
              {(nativeConfirm || nativeRollback) && (
                <Notice
                  tone="warning"
                  title="Native evidence requires review"
                  actions={
                    <Button
                      variant="outline"
                      onClick={() => go('capabilities')}
                    >
                      Inspect capability evidence
                    </Button>
                  }
                >
                  {nativeConfirm ?? nativeRollback}
                </Notice>
              )}
              <div className="mt-5 flex flex-wrap justify-end gap-2">
                <Button
                  variant="outline"
                  disabled={disconnected || !!nativeRollback}
                  onClick={() => act({ type: 'rollback', now: now() })}
                >
                  <Undo2 /> Roll back now
                </Button>
                <Button
                  disabled={
                    state.scenario !== 'normal' ||
                    seconds === 0 ||
                    !!nativeConfirm
                  }
                  onClick={() => act({ type: 'confirm', now: now() })}
                >
                  <CheckCircle2 /> Confirm configuration
                </Button>
              </div>
            </section>
          )}
          {uncertain && (
            <>
              <OutcomeUnknownPanel
                transaction={transaction}
                disconnected={disconnected}
                onReconcile={(result) =>
                  act({ type: 'reconcile', result, now: now() })
                }
              />
              {status !== 'outcome-unknown' && (
                <Notice tone="warning" title={outcomeLabels[status]}>
                  The last reconciliation did not establish a safe final state.
                  New apply remains blocked. Inspect evidence or perform another
                  read after external recovery.
                </Notice>
              )}
            </>
          )}
          {status === 'rollback-conflict' && (
            <Notice
              tone="danger"
              title="Automatic rollback stopped"
              urgent
              actions={
                <Button variant="outline" onClick={() => go('evidence')}>
                  Inspect recovery evidence
                </Button>
              }
            >
              The current native fields or authority no longer match this
              transaction. Overwriting another writer would be unsafe. No force
              rollback is offered; operator recovery is required.
            </Notice>
          )}
          {['confirmed', 'applied', 'not-applied', 'rolled-back'].includes(
            status,
          ) && (
            <Notice
              tone={
                status === 'confirmed' || status === 'applied'
                  ? 'success'
                  : 'info'
              }
              title={outcomeLabels[status]}
              actions={
                <>
                  <Button variant="outline" onClick={() => go('evidence')}>
                    View evidence
                  </Button>
                  <Button variant="outline" onClick={() => go('workspace')}>
                    Open Changes
                  </Button>
                </>
              }
            >
              {status === 'confirmed' || status === 'applied'
                ? 'The final applied state is known in the fixture. The candidate has been cleared.'
                : 'The running configuration was not retained from this change. Candidate intent is available for review and fresh validation.'}
            </Notice>
          )}
        </>
      )}
    </>
  );
}

export function Evidence({ state, go }: Pick<Props, 'state' | 'go'>) {
  const exportEvidence = () => {
    const url = URL.createObjectURL(
      new Blob(
        [
          JSON.stringify(
            {
              prototype: true,
              transaction: state.transaction.id,
              entries: state.evidence,
            },
            null,
            2,
          ),
        ],
        { type: 'application/json' },
      ),
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = 'ovs-prototype-evidence.json';
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  return (
    <>
      <PageHeader
        id="P0-08"
        eyebrow="Evidence / Correlated timeline"
        title="Change evidence"
        description="Events record observations, Jobs record work, and Audit records actions. Correlation keeps them connected."
        actions={
          <Button
            variant="outline"
            disabled={!state.evidence.length}
            onClick={exportEvidence}
          >
            Export evidence
          </Button>
        }
      />
      <div className="mt-5">
        <StatusBadge
          tone={
            ['confirmed', 'applied'].includes(state.transaction.status)
              ? 'success'
              : 'neutral'
          }
        >
          {outcomeLabels[state.transaction.status]}
        </StatusBadge>
      </div>
      <section className="ovs-surface mt-5">
        <h2 className="border-b px-5 py-4 font-semibold">
          Correlated timeline
        </h2>
        {!state.evidence.length ? (
          <p className="p-6 text-sm text-muted-foreground">
            No operations have been recorded in this prototype session.
          </p>
        ) : (
          <ol className="divide-y">
            {state.evidence.map((entry, index) => (
              <li
                key={`${entry.at}-${index}`}
                className="grid gap-2 p-5 text-sm sm:grid-cols-[90px_60px_1fr]"
              >
                <time className="font-mono text-xs text-muted-foreground">
                  {new Date(entry.at).toLocaleTimeString('en-GB')}
                </time>
                <span className="font-semibold text-primary">{entry.kind}</span>
                <div>
                  <p>{entry.text}</p>
                  <p className="mt-2 break-all font-mono text-xs text-muted-foreground">
                    {entry.object} · {entry.correlation}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>
      <Button className="mt-5" variant="outline" onClick={() => go('ports')}>
        Return to ports
      </Button>
    </>
  );
}
