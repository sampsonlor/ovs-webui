'use client';

import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Notice, StatusBadge } from './foundation';
import type { useCoreLab } from '@/hooks/use-core-lab';
import type { Mode, View } from '@/lib/ovs-model';
import { vlanLabel } from '@/lib/ovs-model';
import { fromWireVlan } from '@/lib/api/port-presentation';

type Connection = ReturnType<typeof useCoreLab>;

export function CoreLabTransactionBanner({
  connection: lab,
  onOpen,
}: {
  connection: Connection;
  onOpen: () => void;
}) {
  if (
    !lab.state.transaction?.locksCandidate &&
    !lab.state.pendingTransaction &&
    !lab.state.workspace?.nodeWriteBlocked
  )
    return null;
  return (
    <output className="flex flex-wrap items-center justify-between gap-3 border-t bg-muted px-4 py-3 text-sm">
      <span>
        {lab.state.pendingTransaction
          ? 'OutcomeUnknown · check the original request'
          : lab.state.transaction?.locksCandidate
            ? `Safe Apply · ${lab.state.transaction.safeApply}`
            : 'An unresolved node operation blocks new changes'}
      </span>
      <Button variant="outline" size="sm" onClick={onOpen}>
        Review server operation
      </Button>
    </output>
  );
}

export function CoreLabSafeApplyPanel({
  connection: lab,
  mode,
  view,
  go,
}: {
  connection: Connection;
  mode: Mode;
  view: View;
  go: (view: View) => void;
}) {
  const [reason, setReason] = useState('');
  const [now, setNow] = useState(0);
  const { state, controller } = lab;
  const tx = state.transaction;
  useEffect(() => {
    if (!tx?.locksCandidate) return;
    const timer = setInterval(() => setNow(performance.now()), 250);
    return () => clearInterval(timer);
  }, [tx?.id, tx?.locksCandidate]);
  const presentation = controller?.transactionPresentation(
    Math.max(now, state.transactionReceivedAt),
  );
  const editable =
    state.phase === 'ready' &&
    !lab.checking &&
    !state.pendingRequestId &&
    Boolean(state.workspace?.permissions.editCandidate);
  const hasReason = Boolean(reason.trim()) && reason.length <= 2000;
  const uncertain =
    !['ready', 'writing'].includes(state.phase) ||
    presentation?.state === 'checking-outcome';
  const tone = uncertain
    ? 'uncertain'
    : presentation?.state === 'confirmed' ||
        presentation?.state === 'rolled-back'
      ? 'success'
      : ['rollback-conflict', 'needs-attention', 'degraded'].includes(
            presentation?.state ?? '',
          )
        ? 'danger'
        : 'info';
  return (
    <section
      className="ovs-surface mt-5 p-5"
      aria-labelledby="local-safe-apply-heading"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="local-safe-apply-heading" className="font-semibold">
          {view === 'evidence' ? 'Transaction evidence' : 'Safe Apply'}
        </h2>
        {tx && (
          <StatusBadge tone={tone}>
            {presentation?.state ?? tx.phase}
          </StatusBadge>
        )}
      </div>
      <p className="mt-3 text-sm text-muted-foreground">
        This local workflow applies synthetic Port configuration. The server
        preserves the checkpoint and owns the confirmation deadline.
      </p>
      {uncertain && (
        <Notice tone="uncertain" title="OutcomeUnknown">
          Current evidence is unavailable. Refresh or check the original request
          before sending another command. The server protection window
          continues.
        </Notice>
      )}
      {tx ? (
        <>
          <dl className="mt-4 grid gap-4 text-sm sm:grid-cols-3">
            <div>
              <dt className="text-muted-foreground">Operation</dt>
              <dd className="mt-1">{tx.phase}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Observed outcome</dt>
              <dd className="mt-1">
                {uncertain ? 'Unknown · refresh required' : tx.outcome}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Safe Apply job</dt>
              <dd className="mt-1">
                {state.transactionJob?.state ?? 'Unavailable'}
              </dd>
            </div>
          </dl>
          {tx.locksCandidate && tx.confirmationDeadline && (
            <Notice
              tone="warning"
              title={
                presentation?.remainingSeconds === 0
                  ? 'Server deadline reached'
                  : 'Confirmation window'
              }
            >
              {presentation?.remainingSeconds
                ? `${presentation.remainingSeconds} seconds remaining. Verify management access before confirming.`
                : 'Reading the server decision. A local countdown cannot prove that rollback completed.'}
            </Notice>
          )}
          {['needs-attention', 'rollback-conflict', 'degraded'].includes(
            presentation?.state ?? '',
          ) && (
            <Notice
              tone="danger"
              title={
                tx.safeApply === 'rollback-conflict'
                  ? 'Rollback conflict'
                  : 'Drift or safety degradation'
              }
            >
              Confirmation is blocked. Review the evidence. External changes are
              preserved, and an unresolved operation continues to block new
              configuration.
            </Notice>
          )}
          <dl className="mt-4 grid gap-3 rounded-md border p-4 text-sm sm:grid-cols-2">
            {Object.entries({
              Checkpoint: tx.evidence.checkpoint,
              'Configuration commit': tx.evidence.databaseCommit,
              'Apply observation': tx.evidence.daemonApply,
              'Connectivity probe': tx.evidence.health,
            }).map(([label, value]) => (
              <div key={label}>
                <dt className="text-muted-foreground">{label}</dt>
                <dd className="mt-1">{value}</dd>
              </div>
            ))}
          </dl>
        </>
      ) : (
        <p className="mt-4 text-sm">
          No Safe Apply has started. Review the saved diff and pass the server
          checks first.
        </p>
      )}
      {view !== 'evidence' && (
        <>
          {!tx?.locksCandidate &&
            state.workspace?.latestValidation?.diff.map((change) => (
              <div key={change.id} className="ovs-diff-grid mt-4 text-sm">
                <div>
                  <p className="text-muted-foreground">
                    Before · {change.portId}
                  </p>
                  <p className="mt-2">{vlanLabel(fromWireVlan(change.base))}</p>
                </div>
                <div>
                  <p className="text-muted-foreground">Proposed</p>
                  <p className="mt-2">{vlanLabel(fromWireVlan(change.mine))}</p>
                </div>
              </div>
            ))}
          <label
            className="mt-5 block text-sm font-medium"
            htmlFor="safe-apply-reason"
          >
            Reason for this action
          </label>
          <textarea
            id="safe-apply-reason"
            value={reason}
            maxLength={2000}
            rows={2}
            onChange={(event) => setReason(event.target.value)}
            disabled={!editable}
            className="mt-2 w-full rounded-md border bg-background p-3 text-sm focus-visible:outline-2 focus-visible:outline-primary"
            placeholder="Describe why you are applying, confirming or rolling back this change."
          />
          <div className="mt-4 flex flex-wrap gap-2">
            {!tx?.locksCandidate && (
              <Button
                className="hidden lg:inline-flex"
                disabled={!lab.canStartSafeApply || !hasReason}
                onClick={() => {
                  if (window.matchMedia('(min-width: 1024px)').matches)
                    void controller?.startSafeApply(
                      reason,
                      crypto.randomUUID(),
                    );
                }}
              >
                Apply safely
              </Button>
            )}
            {tx?.locksCandidate && (
              <>
                <Button
                  disabled={
                    !editable || !hasReason || !presentation?.canConfirm
                  }
                  onClick={() =>
                    void controller?.decideSafeApply(
                      'confirm',
                      reason,
                      crypto.randomUUID(),
                    )
                  }
                >
                  Confirm connectivity
                </Button>
                <Button
                  variant="outline"
                  disabled={
                    !editable || !hasReason || !presentation?.canRollback
                  }
                  onClick={() =>
                    void controller?.decideSafeApply(
                      'rollback',
                      reason,
                      crypto.randomUUID(),
                    )
                  }
                >
                  Roll back
                </Button>
                <Button
                  variant="outline"
                  disabled={!editable || !presentation?.canReconcile}
                  onClick={() =>
                    void controller?.reconcileTransaction(crypto.randomUUID())
                  }
                >
                  Check server evidence
                </Button>
              </>
            )}
            <Button variant="outline" onClick={() => go('diff')}>
              Review diff / validation
            </Button>
            <Button variant="outline" onClick={() => go('evidence')}>
              Event / Audit evidence
            </Button>
          </div>
          {!tx?.locksCandidate && (
            <p className="mt-3 text-sm text-muted-foreground lg:hidden">
              Start a new Safe Apply on desktop. An existing transaction can be
              handled here.
            </p>
          )}
        </>
      )}
      {mode === 'expert' && tx && (
        <dl className="mt-4 grid gap-2 text-xs">
          {Object.entries({
            transaction: tx.id,
            request: tx.requestId,
            job: tx.jobId,
            sequence: String(tx.sequence),
            desired: tx.evidence.desiredGeneration ?? 'Unavailable',
            observed: tx.evidence.observedGeneration ?? 'Unavailable',
          }).map(([label, value]) => (
            <div key={label} className="flex flex-wrap gap-2">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="break-all font-mono">{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {view === 'evidence' && (
        <>
          <ol
            className="mt-4 divide-y"
            aria-label="Server transaction evidence"
          >
            {state.evidence?.items.map((entry) => (
              <li key={entry.id} className="py-4 text-sm">
                <p>{entry.summary}</p>
                <p className="mt-2 text-muted-foreground">
                  <time dateTime={entry.occurredAt}>{entry.occurredAt}</time> ·{' '}
                  {entry.kind}
                </p>
                {mode === 'expert' && (
                  <p className="mt-2 break-all font-mono text-xs">
                    {entry.code} · {entry.requestId}
                  </p>
                )}
              </li>
            ))}
          </ol>
          {!state.evidence?.items.length && (
            <p className="mt-4 text-sm text-muted-foreground">
              No transaction evidence is available yet.
            </p>
          )}
          {state.evidence?.nextCursor && (
            <Button
              variant="outline"
              disabled={state.phase !== 'ready'}
              onClick={() => void controller?.loadMoreEvidence()}
            >
              Earlier evidence
            </Button>
          )}
          <Button
            className="mt-4"
            variant="outline"
            onClick={() => go('safe-apply')}
          >
            Review Safe Apply
          </Button>
        </>
      )}
    </section>
  );
}
