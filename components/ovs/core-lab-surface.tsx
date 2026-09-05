'use client';

import { useState } from 'react';
import type { useCoreLab } from '@/hooks/use-core-lab';
import { PortsPage } from './ports-page';
import { VlanEdit } from './vlan-editor';
import { Notice, PageHeader, StatusBadge, ScopeBadge } from './foundation';
import { Button } from '@/components/ui/button';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import {
  fromWireVlan,
  portEditable,
  portPresentation,
  toWireVlan,
} from '@/lib/api/port-presentation';
import { vlanLabel, type Mode, type View } from '@/lib/ovs-model';
import type {
  CandidateMutation,
  PortResource,
  VlanIntent,
} from '@/lib/api/types.generated';

type Connection = ReturnType<typeof useCoreLab>;
export const coreLabViews: View[] = [
  'ports',
  'port-detail',
  'vlan-edit',
  'workspace',
  'diff',
  'safe-apply',
  'evidence',
];

export function CoreLabSession({
  connection: lab,
}: {
  connection: Connection;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b bg-muted/60 px-4 py-2 text-sm">
      <span className="mr-auto">
        Local integration · synthetic Ports · Candidate saved on server{' '}
        {lab.session ? `· ${lab.session.label}` : ''}
      </span>
      {(['alice', 'bob', 'observer'] as const).map((principal) => (
        <Button
          key={principal}
          variant="outline"
          size="sm"
          disabled={lab.checking}
          onClick={() => void lab.connect(principal)}
        >
          {principal === 'alice'
            ? 'Editor A'
            : principal === 'bob'
              ? 'Editor B'
              : 'Read-only'}
        </Button>
      ))}
      {lab.session && (
        <Button
          variant="ghost"
          size="sm"
          disabled={lab.checking}
          onClick={() => void lab.connect(undefined, true)}
        >
          Sign out
        </Button>
      )}
      {lab.session?.editable && (
        <label className="flex items-center gap-2 text-sm">
          Simulate observation
          <NativeSelect
            value=""
            aria-label="Local observation fixture"
            disabled={
              lab.checking ||
              lab.state.phase === 'writing' ||
              Boolean(lab.state.pendingRequestId)
            }
            onChange={(event) => void lab.observe(event.target.value)}
          >
            <NativeSelectOption value="">Choose…</NativeSelectOption>
            {[
              ['stale', 'Generation change'],
              ['conflict', 'VLAN conflict'],
              ['provider-unavailable', 'Provider unavailable'],
              ['node-blocked', 'Unresolved node operation'],
              ['healthy', 'Restore provider / release node lock'],
            ].map(([value, label]) => (
              <NativeSelectOption key={value} value={value}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
      )}
    </div>
  );
}

function LabVlanEditor({
  port,
  initial,
  mode,
  locked,
  onSave,
  go,
}: {
  port: PortResource;
  initial: VlanIntent;
  mode: Mode;
  locked: boolean;
  onSave: (value: VlanIntent) => void;
  go: (view: View) => void;
}) {
  const [vlanMode, setVlanMode] = useState(initial.mode);
  const [trunks, setTrunks] = useState(initial.trunks.join(', '));
  const [tag, setTag] = useState(
    initial.tag === null ? '' : String(initial.tag),
  );
  return (
    <VlanEdit
      port={portPresentation(port)}
      mode={mode}
      vlanMode={vlanMode}
      setVlanMode={(value) => setVlanMode(value as VlanIntent['mode'])}
      allowedVlans={trunks}
      setAllowedVlans={setTrunks}
      nativeTag={tag}
      setNativeTag={setTag}
      locked={locked}
      onStage={() =>
        onSave(
          toWireVlan({
            mode: vlanMode,
            tag: vlanMode === 'trunk' ? null : Number(tag),
            trunks: vlanMode === 'access' ? '' : trunks,
          }),
        )
      }
      go={go}
    />
  );
}

export function CoreLabSurface({
  connection: lab,
  view,
  mode,
  selected,
  setSelected,
  go,
}: {
  connection: Connection;
  view: View;
  mode: Mode;
  selected: string;
  setSelected: (name: string) => void;
  go: (view: View) => void;
}) {
  const [search, setSearch] = useState('');
  const [feedback, setFeedback] = useState('');
  const { state, controller } = lab;
  const ports = state.inventory?.items ?? [];
  const port = ports.find((item) => item.name === selected);
  const candidate = state.snapshot?.candidate;
  const intent = candidate?.intents[0];
  const submit = async (command: CandidateMutation) => {
    if (!window.matchMedia('(min-width: 1024px)').matches) {
      setFeedback('New Candidate changes require desktop.');
      return;
    }
    setFeedback('');
    if (await controller?.mutate(command)) go('workspace');
  };
  const refresh = () => {
    setFeedback('');
    void controller?.refresh();
  };
  if (lab.checking || (state.phase === 'loading' && !state.snapshot))
    return (
      <Notice tone="info" title="Restoring workspace">
        Reading identity, saved Candidate and inventory before enabling edits.
      </Notice>
    );
  if (!lab.session || state.phase === 'signed-out')
    return (
      <Notice
        tone="info"
        title="Choose a local review user"
        actions={
          <Button variant="outline" onClick={() => void lab.connect()}>
            Refresh session
          </Button>
        }
      >
        Editor A and Editor B have separate saved Candidates. Read-only can
        inspect but cannot stage changes. {lab.error}
      </Notice>
    );
  const banner = (
    <>
      {(state.message || feedback || lab.error) && (
        <Notice
          tone={
            state.phase === 'unknown'
              ? 'uncertain'
              : state.phase === 'error'
                ? 'warning'
                : 'info'
          }
          title={
            state.phase === 'unknown'
              ? 'Candidate save outcome unknown'
              : 'Workspace status'
          }
          actions={
            <Button
              variant="outline"
              disabled={state.phase === 'writing' || state.phase === 'loading'}
              onClick={refresh}
            >
              {state.pendingRequestId
                ? 'Check original request'
                : 'Refresh workspace'}
            </Button>
          }
        >
          {feedback || lab.error || state.message}
          {state.pendingRequestId && (
            <p className="mt-2 font-mono text-xs">{state.pendingRequestId}</p>
          )}
        </Notice>
      )}
      {!state.workspace?.permissions.editCandidate && (
        <Notice tone="info" title="Read-only access">
          This user can inspect saved state. Standard and Expert do not change
          permissions.
        </Notice>
      )}
      {state.workspace?.nodeWriteBlocked && (
        <Notice tone="uncertain" title="Node operation unresolved">
          New edits remain blocked until the server resolves the existing
          operation.
        </Notice>
      )}
    </>
  );
  if (state.permissionError || !candidate)
    return (
      <>
        {banner}
        <Notice tone="warning" title="Workspace unavailable">
          A successful server response is required before displaying editable
          data.
        </Notice>
      </>
    );
  if (view === 'ports')
    return (
      <>
        {banner}
        <PortsPage
          identityLabel="Port ID"
          inventory={ports.map(portPresentation)}
          snapshotLabel={`Server snapshot · ${state.inventory?.observedAt}`}
          mode={mode}
          search={search}
          setSearch={setSearch}
          selected={selected}
          setSelected={setSelected}
          go={go}
          onRefresh={refresh}
          scenario={
            state.phase === 'error'
              ? 'error'
              : state.inventory?.availability === 'degraded'
                ? 'degraded'
                : !ports.length
                  ? 'empty'
                  : 'normal'
          }
        />
      </>
    );
  if (view === 'port-detail') {
    if (!port)
      return (
        <>
          {banner}
          <Notice
            tone="warning"
            title="Port no longer in the snapshot"
            actions={<Button onClick={() => go('ports')}>Open Ports</Button>}
          >
            Refresh the current inventory before selecting another object.
          </Notice>
        </>
      );
    const display = portPresentation(port);
    return (
      <>
        {banner}
        <PageHeader
          id="P0-03"
          eyebrow="Switching / Port detail"
          title={port.name}
          description="Current server observation and native object identity."
          actions={
            <>
              <Button variant="outline" onClick={() => go('ports')}>
                Ports
              </Button>
              <Button
                disabled={!lab.canWrite || !portEditable(port)}
                onClick={() => go('vlan-edit')}
              >
                Edit VLAN
              </Button>
            </>
          }
        />
        <section className="ovs-surface mt-5 p-5">
          <div className="flex gap-3">
            <ScopeBadge scope={display.scope} />
            <StatusBadge
              tone={port.linkState === 'unknown' ? 'uncertain' : 'neutral'}
            >
              {display.state}
            </StatusBadge>
          </div>
          <dl className="mt-5 grid gap-5 sm:grid-cols-2">
            {[
              ['Bridge', port.bridgeId],
              ['Port', port.name],
              ['Interfaces', display.interfaceName],
              ['VLAN', display.vlan],
              ['Speed', display.speed],
              ['Authority', display.authority],
              ['Provider', port.provider],
              ['Observed', port.observedAt],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-sm text-muted-foreground">{label}</dt>
                <dd className="mt-1 break-words text-sm">{value}</dd>
              </div>
            ))}
          </dl>
          {mode === 'expert' && (
            <pre className="mt-5 overflow-auto rounded border bg-muted p-3 text-xs">
              {JSON.stringify(
                {
                  id: port.id,
                  generation: port.generation,
                  configuration: port.configuration,
                  writableFields: port.writableFields,
                },
                null,
                2,
              )}
            </pre>
          )}
        </section>
      </>
    );
  }
  if (view === 'vlan-edit') {
    if (
      !port ||
      !portEditable(port) ||
      port.configuration.availability !== 'known'
    )
      return (
        <>
          {banner}
          <Notice tone="warning" title="VLAN editing unavailable">
            The selected Port must have known configuration and native write
            authority.
          </Notice>
        </>
      );
    const initial =
      candidate.intents.find((item) => item.portId === port.id)?.mine ??
      port.configuration.value;
    return (
      <>
        {banner}
        <LabVlanEditor
          key={`${port.id}:${candidate.revision}`}
          port={port}
          initial={initial}
          mode={mode}
          locked={!lab.canWrite || candidate.freshness !== 'current'}
          go={go}
          onSave={(mine) =>
            void submit({
              operation: 'set-vlan',
              requestId: crypto.randomUUID(),
              portId: port.id,
              mine,
              expectedGeneration: port.generation,
            })
          }
        />
      </>
    );
  }
  if (view === 'workspace' || view === 'diff')
    return (
      <>
        {banner}
        <PageHeader
          id={view === 'workspace' ? 'P0-05' : 'P0-06'}
          eyebrow="Global / Changes"
          title={view === 'workspace' ? 'Candidate workspace' : 'Diff review'}
          description="Saved for the current user. Running configuration changes only after a separate validated apply."
          actions={
            <Button variant="outline" onClick={() => go('ports')}>
              Browse Ports
            </Button>
          }
        />
        <div className="mt-5 flex flex-wrap gap-3">
          <StatusBadge
            tone={candidate.freshness === 'current' ? 'info' : 'warning'}
          >
            {candidate.freshness}
          </StatusBadge>
          <span className="font-mono text-sm">{candidate.revision}</span>
        </div>
        {!intent ? (
          <Notice tone="neutral" title="No pending changes">
            The server returned an empty Candidate. Refreshing the browser
            preserves this workspace.
          </Notice>
        ) : (
          <section className="ovs-surface mt-5 p-5">
            <h2 className="font-semibold">
              Port /{' '}
              {ports.find((item) => item.id === intent.portId)?.name ??
                intent.portId}{' '}
              · VLAN
            </h2>
            <div className="ovs-diff-grid">
              {[
                { name: 'Base', value: intent.base },
                ...(candidate.conflicts[0]
                  ? [
                      {
                        name: 'Current system',
                        value: candidate.conflicts[0].current,
                      },
                    ]
                  : []),
                { name: 'Your change', value: intent.mine },
              ].map((item) => (
                <div key={item.name}>
                  <p className="text-sm text-muted-foreground">{item.name}</p>
                  <p className="mt-3 text-sm font-medium">
                    {vlanLabel(fromWireVlan(item.value))}
                  </p>
                  {mode === 'expert' && (
                    <pre className="mt-3 whitespace-pre-wrap font-mono text-xs">
                      {JSON.stringify(item.value, null, 2)}
                    </pre>
                  )}
                </div>
              ))}
            </div>
            <p className="mt-3 break-all font-mono text-xs text-muted-foreground">
              Base {candidate.baseGeneration} · Current{' '}
              {candidate.currentGeneration}
            </p>
          </section>
        )}
        {candidate.freshness !== 'current' && (
          <Notice
            tone={candidate.freshness === 'conflict' ? 'danger' : 'warning'}
            title={
              candidate.freshness === 'conflict'
                ? 'The same VLAN field changed'
                : 'Candidate base is stale'
            }
          >
            Review current state before rebasing. Your choice changes saved
            intent and requires fresh validation.
          </Notice>
        )}
        <div className="mt-5 hidden flex-wrap gap-3 lg:flex">
          {candidate.freshness === 'conflict'
            ? (['current', 'mine'] as const).map((choice) => (
                <Button
                  key={choice}
                  variant={choice === 'current' ? 'outline' : 'default'}
                  disabled={!lab.canWrite}
                  onClick={() =>
                    void submit({
                      requestId: crypto.randomUUID(),
                      operation: 'rebase',
                      currentGeneration: candidate.currentGeneration,
                      conflictSnapshotId: candidate.conflictSnapshotId,
                      resolutions: candidate.conflicts.map((item) => ({
                        intentId: item.intentId,
                        choice,
                      })),
                    })
                  }
                >
                  {choice === 'current'
                    ? 'Keep current system value'
                    : 'Use my value in Candidate'}
                </Button>
              ))
            : candidate.freshness === 'stale' && (
                <Button
                  disabled={!lab.canWrite}
                  onClick={() =>
                    void submit({
                      requestId: crypto.randomUUID(),
                      operation: 'rebase',
                      currentGeneration: candidate.currentGeneration,
                      conflictSnapshotId: null,
                      resolutions: [],
                    })
                  }
                >
                  Rebase non-overlapping change
                </Button>
              )}
          <Button
            variant="outline"
            disabled={!lab.canWrite || !intent}
            onClick={() =>
              void submit({
                requestId: crypto.randomUUID(),
                operation: 'discard',
              })
            }
          >
            Discard saved intent
          </Button>
          <Button
            variant="outline"
            onClick={() => go(view === 'workspace' ? 'diff' : 'workspace')}
          >
            {view === 'workspace' ? 'Review diff' : 'Workspace'}
          </Button>
        </div>
        <Notice tone="info" title="Validation service pending">
          This local slice saves and restores Candidate only. Safe Apply becomes
          available after the server supplies validation, checkpoint and
          rollback protection.
        </Notice>
      </>
    );
  return (
    <>
      {banner}
      <PageHeader
        id={view === 'safe-apply' ? 'P0-07' : 'P0-08'}
        eyebrow="Operations"
        title={view === 'safe-apply' ? 'Safe Apply' : 'Request evidence'}
        description="Authoritative server operation state."
      />
      <Notice
        tone="info"
        title={
          state.workspace?.activeTransactions.length
            ? 'Existing server operation'
            : 'No apply operation started'
        }
      >
        The persistence lab does not execute OVS mutations. Its request ledger
        can recover Candidate saves; transaction execution and Event/Audit views
        will be connected in the next server slice.
      </Notice>
      {state.workspace?.activeTransactions.map((transaction) => (
        <div key={transaction.id} className="ovs-surface mt-3 p-4">
          <p className="font-mono">{transaction.id}</p>
          <StatusBadge tone="uncertain">
            {transaction.knowledge} · {transaction.safeApply}
          </StatusBadge>
        </div>
      ))}
      <Button variant="outline" onClick={() => go('workspace')}>
        Review saved Candidate
      </Button>
    </>
  );
}
