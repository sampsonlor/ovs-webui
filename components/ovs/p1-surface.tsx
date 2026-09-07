'use client';

import { useEffect, useRef, useState } from 'react';
import { P1SwitchingView } from '@/app/p1-switching';
import { P1DiagnosticsView } from '@/app/p1-diagnostics';
import { P1OpenFlowView } from '@/app/p1-openflow';
import {
  diagnosticJobLabels,
  openFlowReviewLabels,
  p1Views,
  type ChangeIntent,
  type DiagnosticInputState,
  type DiagnosticJobState,
  type DiagnosticParameters,
  type DiagnosticRequest,
  type OpenFlowReviewState,
  type P1View,
  type ReviewScenario,
} from '@/app/prototype-model';
import { captureDiagnosticRequest, diagnosticRunBlock } from '@/lib/p1-control';
import type {
  ControlAction,
  ControlState,
  Scenario,
} from '@/lib/change-control';
import type { Mode, View } from '@/lib/ovs-model';
import { Notice, StatusBadge } from './foundation';
import { Button } from '@/components/ui/button';

export function isP1View(view: View): view is P1View {
  return p1Views.some((item) => item === view);
}

function switchingScenario(scenario: Scenario): ReviewScenario {
  if (scenario === 'degraded' || scenario === 'provider-unavailable')
    return 'provider-degraded';
  if (scenario === 'stale' || scenario === 'conflict') return 'drift';
  if (
    [
      'normal',
      'drift',
      'member-down',
      'lacp-mismatch',
      'validation-blocked',
      'provider-degraded',
      'advanced-config',
      'outcome-unknown',
      'network-loss',
    ].includes(scenario)
  )
    return scenario as ReviewScenario;
  return 'normal';
}

// Keep selections and diagnostic Jobs mounted while configuration uses the shared workspace.
export function useP1Controller({
  act,
  go,
  notify,
  scenario,
  openPort,
}: {
  act: (action: ControlAction) => ControlState;
  go: (view: View) => void;
  notify: (message: string) => void;
  scenario: Scenario;
  openPort: (name: string) => void;
}) {
  const [selectedBridge, setSelectedBridge] = useState('br-fabric');
  const [selectedBond, setSelectedBond] = useState('bond-uplink');
  const [search, setSearch] = useState('');
  const [selectedDiagnostic, setSelectedDiagnostic] =
    useState('diag.net.link-lacp');
  const [inputState, setInputState] = useState<DiagnosticInputState>('valid');
  const [jobState, setJobState] = useState<DiagnosticJobState>('not-started');
  const [scope, setScope] = useState('Port/bond-storage');
  const [parameters, setParameters] = useState<DiagnosticParameters>({
    sampleSeconds: 10,
    detail: 'structured',
  });
  const [request, setRequest] = useState<DiagnosticRequest | null>(null);
  const requestRef = useRef<DiagnosticRequest | null>(null);
  const saveRequest = (next: DiagnosticRequest) => {
    requestRef.current = next;
    setRequest(next);
  };
  const [origin, setOrigin] = useState<string | null>(null);
  const [autoAdvance, setAutoAdvance] = useState(false);
  const [openFlowState, setOpenFlowState] =
    useState<OpenFlowReviewState>('fresh');
  const jobRef = useRef(jobState);
  const handlers = useRef({ act, notify });
  useEffect(() => {
    handlers.current = { act, notify };
  });
  const updateJob = (next: DiagnosticJobState) => {
    jobRef.current = next;
    setJobState(next);
  };
  const record = (kind: 'Audit' | 'Event' | 'Job', text: string) =>
    act({
      type: 'record-evidence',
      kind,
      text,
      object: requestRef.current?.scope ?? scope,
      correlation: 'corr-DIAG-91C4',
      now: Date.now(),
    });
  const stageIntent = (intent: ChangeIntent) => {
    const next = act({
      type: 'stage-topology',
      intent,
      desktop: window.matchMedia('(min-width: 1024px)').matches,
      now: Date.now(),
    });
    if (next.error) return next;
    if (intent.bridgeName) setSelectedBridge(intent.bridgeName);
    if (intent.kind === 'bond') setSelectedBond(intent.objectName);
    go('workspace');
    return next;
  };
  const openDiagnostics = (
    nextScope: string,
    source = `Object / ${nextScope}`,
  ) => {
    if (['queued', 'running', 'cancel-requested'].includes(jobRef.current)) {
      notify('Open the active diagnostic Job before changing its scope.');
      go('diagnostic-run');
      return;
    }
    setScope(nextScope);
    setOrigin(source);
    go('diagnostics-hub');
  };
  const runDiagnostic = (
    id = selectedDiagnostic,
    target = scope,
    options = parameters,
  ) => {
    const blocked = diagnosticRunBlock(
      id,
      target,
      inputState,
      ['queued', 'running', 'cancel-requested'].includes(jobRef.current),
      window.innerWidth,
    );
    if (blocked) {
      notify(blocked);
      return blocked;
    }
    if (
      ['permission-denied', 'provider-unavailable', 'network-loss'].includes(
        scenario,
      )
    ) {
      notify('Diagnostic service is unavailable in this review state.');
      return 'Diagnostic service unavailable.';
    }
    let submitted: DiagnosticRequest;
    try {
      submitted = captureDiagnosticRequest(id, target, options);
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : 'Invalid diagnostic parameters.';
      notify(message);
      return message;
    }
    saveRequest(submitted);
    setSelectedDiagnostic(id);
    setScope(target);
    setParameters({
      sampleSeconds: submitted.sampleSeconds,
      detail: submitted.detail,
    });
    updateJob('queued');
    setAutoAdvance(true);
    act({
      type: 'record-evidence',
      kind: 'Audit',
      text: `Synthetic bounded diagnostic requested · ${id} · ${submitted.sampleSeconds}s · ${submitted.detail} · job-3114`,
      object: target,
      correlation: 'corr-DIAG-91C4',
      now: Date.now(),
    });
    go('diagnostic-run');
    return null;
  };
  const cancelDiagnostic = () => {
    if (
      !['queued', 'running'].includes(jobRef.current) ||
      requestRef.current?.id === 'diag.openflow.collection'
    ) {
      notify('No safe cancellation checkpoint is available.');
      return 'No safe cancellation checkpoint.';
    }
    updateJob('cancel-requested');
    setAutoAdvance(true);
    record('Audit', 'Synthetic diagnostic cancellation requested · job-3114');
    return null;
  };
  const reviewDiagnostic = (next: DiagnosticJobState) => {
    if (!requestRef.current)
      saveRequest({ id: selectedDiagnostic, scope, ...parameters });
    setAutoAdvance(false);
    updateJob(next);
    go('diagnostic-run');
  };
  const reviewOpenFlow = (next: OpenFlowReviewState) => {
    setOpenFlowState(next);
    go('openflow-viewer');
  };
  const openEvidence = (kind: 'event' | 'audit') => {
    record(
      kind === 'event' ? 'Event' : 'Audit',
      `Diagnostic review fixture · job-3114 · ${diagnosticJobLabels[jobRef.current]} · ${kind}`,
    );
    go('evidence');
  };
  const openObject = (target: string) => {
    const [kind, name] = target.split('/');
    if (kind === 'Bridge') {
      setSelectedBridge(name);
      go('bridge-detail');
    } else if (name.startsWith('bond-')) {
      setSelectedBond(name);
      go('bond-detail');
    } else openPort(name);
  };
  useEffect(() => {
    if (
      !autoAdvance ||
      !['queued', 'running', 'cancel-requested'].includes(jobState)
    )
      return;
    const next =
      jobState === 'queued'
        ? 'running'
        : jobState === 'cancel-requested'
          ? 'cancelled'
          : 'complete';
    const timer = window.setTimeout(
      () => {
        jobRef.current = next;
        setJobState(next);
        handlers.current.act({
          type: 'record-evidence',
          kind: next === 'running' ? 'Job' : 'Event',
          text: `Synthetic diagnostic · job-3114 · ${diagnosticJobLabels[next]}`,
          object: request?.scope ?? scope,
          correlation: 'corr-DIAG-91C4',
          now: Date.now(),
        });
        handlers.current.notify(
          `Diagnostic fixture · ${diagnosticJobLabels[next]}`,
        );
      },
      jobState === 'running' ? (request?.sampleSeconds ?? 10) * 1000 : 800,
    );
    return () => window.clearTimeout(timer);
  }, [autoAdvance, jobState, request, scope]);

  const updateInput = (update: () => void) => {
    if (['queued', 'running', 'cancel-requested'].includes(jobRef.current)) {
      notify('Wait for the active diagnostic Job before changing its input.');
      return;
    }
    update();
  };

  return {
    selectedBridge,
    selectedBond,
    search,
    setSearch,
    setSelectedBridge,
    setSelectedBond,
    selectedDiagnostic,
    setSelectedDiagnostic: (id: string) =>
      updateInput(() => setSelectedDiagnostic(id)),
    inputState,
    setInputState,
    jobState,
    scope,
    setScope: (target: string) =>
      updateInput(() => {
        setScope(target);
        setOrigin(null);
      }),
    parameters,
    setParameters: (next: DiagnosticParameters) =>
      updateInput(() => setParameters(next)),
    request,
    origin,
    clearOrigin: () => setOrigin(null),
    openFlowState,
    setOpenFlowState,
    stageIntent,
    openDiagnostics,
    runDiagnostic,
    cancelDiagnostic,
    reviewDiagnostic,
    reviewOpenFlow,
    openEvidence,
    openObject,
    busy: ['queued', 'running', 'cancel-requested'].includes(jobState),
  };
}

type P1Controller = ReturnType<typeof useP1Controller>;

export function P1Surface({
  view,
  mode,
  scenario,
  controller: p1,
  go,
  notify,
}: {
  view: P1View;
  mode: Mode;
  scenario: Scenario;
  controller: P1Controller;
  go: (view: View) => void;
  notify: (message: string) => void;
}) {
  if (
    [
      'loading',
      'empty',
      'error',
      'permission-denied',
      'provider-unavailable',
    ].includes(scenario)
  )
    return (
      <Notice
        tone={
          scenario === 'loading' || scenario === 'empty' ? 'neutral' : 'warning'
        }
        title={`Inventory · ${scenario}`}
      >
        Current object data is withheld in this review state. Choose Normal path
        to resume the synthetic fixture.
      </Notice>
    );
  if (view === 'openflow-viewer')
    return (
      <P1OpenFlowView
        mode={mode}
        reviewState={p1.openFlowState}
        setReviewState={p1.setOpenFlowState}
        openBridge={(name) => p1.openObject(`Bridge/${name}`)}
        openPort={(name) => p1.openObject(`Port/${name}`)}
        go={go}
        notify={notify}
      />
    );
  if (view === 'diagnostics-hub' || view === 'diagnostic-run')
    return (
      <P1DiagnosticsView
        view={view}
        mode={mode}
        selectedDiagnostic={p1.selectedDiagnostic}
        setSelectedDiagnostic={p1.setSelectedDiagnostic}
        inputState={p1.inputState}
        setInputState={p1.setInputState}
        jobState={p1.jobState}
        setJobState={p1.reviewDiagnostic}
        scope={p1.scope}
        setScope={p1.setScope}
        parameters={p1.parameters}
        setParameters={p1.setParameters}
        request={p1.request}
        busy={p1.busy}
        origin={p1.origin}
        clearOrigin={p1.clearOrigin}
        runDiagnostic={() => p1.runDiagnostic()}
        cancelDiagnostic={p1.cancelDiagnostic}
        retryDiagnostic={() =>
          p1.request &&
          p1.runDiagnostic(p1.request.id, p1.request.scope, p1.request)
        }
        openEvidence={p1.openEvidence}
        openObject={() => p1.openObject(p1.request?.scope ?? p1.scope)}
        go={go}
        notify={notify}
      />
    );
  return (
    <P1SwitchingView
      view={view}
      mode={mode}
      scenario={switchingScenario(scenario)}
      search={p1.search}
      setSearch={p1.setSearch}
      selectedBridge={p1.selectedBridge}
      setSelectedBridge={p1.setSelectedBridge}
      selectedBond={p1.selectedBond}
      setSelectedBond={p1.setSelectedBond}
      go={go}
      onStageIntent={p1.stageIntent}
      onDiagnose={p1.openDiagnostics}
    />
  );
}

export function P1MobileSummary({
  view,
  controller: p1,
  go,
}: {
  view: P1View;
  controller: P1Controller;
  go: (view: View) => void;
}) {
  return (
    <section className="ovs-surface p-5">
      <p className="ovs-eyebrow">Incident companion · synthetic snapshot</p>
      <h2 className="mt-2 text-xl font-semibold">
        {view === 'openflow-viewer'
          ? 'OpenFlow collection'
          : view.startsWith('diagnostic')
            ? 'Diagnostic Job'
            : 'Switching posture'}
      </h2>
      <div className="mt-4">
        <StatusBadge tone="neutral">
          {view === 'openflow-viewer'
            ? openFlowReviewLabels[p1.openFlowState]
            : view.startsWith('diagnostic')
              ? diagnosticJobLabels[p1.jobState]
              : 'Last known inventory'}
        </StatusBadge>
      </div>
      <p className="mt-3 text-sm">
        {view.startsWith('diagnostic')
          ? (p1.request?.scope ?? p1.scope)
          : p1.selectedBridge}
      </p>
      <p className="mt-3 text-sm text-muted-foreground">
        Review incident evidence here. New configuration requires desktop;
        bounded diagnostics and OpenFlow queries require tablet or desktop.
      </p>
      <Button className="mt-4" variant="outline" onClick={() => go('evidence')}>
        Open evidence
      </Button>
    </section>
  );
}
