'use client';

import { Button } from '@/components/ui/button';
import { Notice, StatusBadge } from './foundation';
import { fromWireVlan } from '@/lib/api/port-presentation';
import { vlanLabel, type Mode } from '@/lib/ovs-model';
import type { WorkspaceState } from '@/lib/api/workspace-controller';

export function CoreLabValidationPanel({
  state,
  mode,
  canValidate,
  onValidate,
  onSafeApply,
}: {
  state: WorkspaceState;
  mode: Mode;
  canValidate: boolean;
  onValidate: () => void;
  onSafeApply: () => void;
}) {
  const validation = state.workspace?.latestValidation;
  const unavailable = ['error', 'unknown'].includes(state.phase);
  const busy = state.phase === 'writing' && Boolean(state.pendingValidation);
  return (
    <section
      className="ovs-surface mt-5 p-5"
      aria-labelledby="server-validation-heading"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="server-validation-heading" className="font-semibold">
          Server Diff / Validation
        </h2>
        <Button
          className="hidden lg:inline-flex"
          disabled={!canValidate}
          onClick={onValidate}
        >
          {busy
            ? 'Requesting validation…'
            : validation
              ? 'Validate again'
              : 'Run validation'}
        </Button>
      </div>
      {!validation ? (
        <p className="mt-3 text-sm text-muted-foreground">
          Save and review a current Candidate, then request server checks. Each
          result belongs to one saved version.
        </p>
      ) : (
        <>
          <div
            className="mt-4 flex flex-wrap items-center gap-3"
            aria-live="polite"
          >
            <StatusBadge
              tone={
                unavailable
                  ? 'uncertain'
                  : validation.status === 'passed'
                    ? 'success'
                    : validation.status === 'blocked'
                      ? 'danger'
                      : validation.status === 'expired'
                        ? 'warning'
                        : 'info'
              }
            >
              {unavailable
                ? `Last read: ${validation.status}`
                : validation.status === 'passed'
                  ? 'Passed · synthetic checks'
                  : validation.status}
            </StatusBadge>
            {state.validationJob && (
              <span className="text-sm text-muted-foreground">
                Validation job: {state.validationJob.state}
              </span>
            )}
          </div>
          {unavailable && (
            <Notice tone="uncertain" title="Validation status unavailable">
              The last response is retained for reference. Refresh the workspace
              or check the original request to obtain current server evidence.
            </Notice>
          )}
          {['pending', 'running'].includes(validation.status) && (
            <p className="mt-3 text-sm">
              The server is checking this snapshot. You can return later to read
              its result.
            </p>
          )}
          {validation.status === 'expired' && (
            <Notice tone="warning" title="Validation expired">
              This diff is retained for review. Review the current Candidate and
              run a new validation before proceeding.
            </Notice>
          )}
          {validation.diff.map((change) => (
            <div key={change.id} className="mt-4">
              <p className="text-sm font-medium">
                Captured Port / {change.portId} · VLAN
              </p>
              <div className="ovs-diff-grid">
                {[
                  { label: 'Before', value: change.base },
                  { label: 'Proposed', value: change.mine },
                ].map(({ label, value }) => (
                  <div key={label}>
                    <p className="text-sm text-muted-foreground">{label}</p>
                    <p className="mt-2 text-sm">
                      {vlanLabel(fromWireVlan(value))}
                    </p>
                  </div>
                ))}
              </div>
            </div>
          ))}
          <ul className="mt-4 divide-y" aria-label="Server validation checks">
            {validation.checks.map((check) => (
              <li
                key={`${check.code}:${check.portId}`}
                className="flex items-start gap-3 py-3 text-sm"
              >
                <StatusBadge
                  tone={
                    check.state === 'pass'
                      ? 'success'
                      : check.state === 'block'
                        ? 'danger'
                        : check.state === 'unknown'
                          ? 'uncertain'
                          : 'warning'
                  }
                >
                  {check.state}
                </StatusBadge>
                <div className="min-w-0">
                  <p>{check.message}</p>
                  {mode === 'expert' && (
                    <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
                      {check.code}
                      {check.portId ? ` · ${check.portId}` : ''}
                    </p>
                  )}
                </div>
              </li>
            ))}
          </ul>
          {validation.safetyPlan && (
            <dl className="mt-4 grid gap-3 rounded-md border p-4 text-sm sm:grid-cols-3">
              {[
                ['Checkpoint', validation.safetyPlan.checkpoint],
                ['Connectivity probe', validation.safetyPlan.connectivityProbe],
                [
                  'Compare before rollback',
                  validation.safetyPlan.compareBeforeRollback,
                ],
              ].map(([label, value]) => (
                <div key={label}>
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="mt-1 font-medium">{value}</dd>
                </div>
              ))}
            </dl>
          )}
          {validation.expiresAt && (
            <p className="mt-4 text-sm text-muted-foreground">
              Server validity deadline: {validation.expiresAt}
            </p>
          )}
          {mode === 'expert' && (
            <dl className="mt-4 grid gap-2 text-xs">
              {Object.entries({
                validation: validation.id,
                request: validation.requestId,
                job: validation.jobId,
                candidate: validation.candidateId,
                revision: validation.candidateRevision,
                generation: validation.generation,
                policy: validation.policyRevision,
              }).map(([label, value]) => (
                <div key={label} className="flex flex-wrap gap-2">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="break-all font-mono">{value}</dd>
                </div>
              ))}
            </dl>
          )}
        </>
      )}
      <Notice tone="info" title="Safe Apply prerequisites">
        Validation checks the synthetic safety capabilities. Safe Apply rechecks
        this snapshot, creates its own checkpoint and starts a server-owned
        protection window. Passing validation does not apply configuration.
      </Notice>
      <Button className="mt-4" variant="outline" onClick={onSafeApply}>
        Review Safe Apply
      </Button>
      <p className="mt-3 text-sm text-muted-foreground lg:hidden">
        Use desktop to request a new validation. Saved results remain available
        for review here.
      </p>
    </section>
  );
}
