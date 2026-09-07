'use client';

import { useEffect, useRef, useState } from 'react';
import { P1SwitchingView } from '@/app/p1-switching';
import { P1DiagnosticsView } from '@/app/p1-diagnostics';
import { P1OpenFlowView } from '@/app/p1-openflow';
import {
  diagnosticJobLabels,
  p1Views,
  p1SwitchingPages,
  type ChangeIntent,
  type DiagnosticInputState,
  type DiagnosticJobState,
  type DiagnosticParameters,
  type DiagnosticRequest,
  type OpenFlowReviewState,
  type P1View,
} from '@/app/prototype-model';
import { captureDiagnosticRequest, diagnosticRunBlock } from '@/lib/p1-control';
import {
  diagnosticActive,
  diagnosticCancelBlock,
  diagnosticServiceBlock,
} from '@/lib/diagnostics-model';
import type {
  ControlAction,
  ControlState,
  Scenario,
} from '@/lib/change-control';
import type { Mode, View } from '@/lib/ovs-model';
import { ports } from '@/lib/ovs-model';
import { bridges, bonds } from '@/lib/switching-model';
import { StatusBadge } from './foundation';
import { Button } from '@/components/ui/button';
import { useOpenFlowController } from './openflow-controller';

export function isP1View(view: View): view is P1View {
  return p1Views.some((item) => item === view);
}

// Keep selections and diagnostic Jobs mounted while configuration uses the shared workspace.
export function useP1Controller({
  act,
  go,
  notify,
  scenario,
  getScenario,
  openPort,
}: {
  act: (action: ControlAction) => ControlState;
  go: (view: View) => void;
  notify: (message: string) => void;
  scenario: Scenario;
  getScenario: () => Scenario;
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
  const [reviewOnly, setReviewOnly] = useState(false);
  const reviewOnlyRef = useRef(false);
  const updateReviewOnly = (next: boolean) => {
    reviewOnlyRef.current = next;
    setReviewOnly(next);
  };
  const requestRef = useRef<DiagnosticRequest | null>(null);
  const saveRequest = (next: DiagnosticRequest) => {
    requestRef.current = next;
    setRequest(next);
  };
  const [origin, setOrigin] = useState<string | null>(null);
  const [autoAdvance, setAutoAdvance] = useState(false);
  const openFlow = useOpenFlowController(scenario, getScenario, notify);
  const jobRef = useRef(jobState);
  const handlers = useRef({ act, notify, getScenario });
  useEffect(() => {
    handlers.current = { act, notify, getScenario };
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
    requestInput: DiagnosticInputState = inputState,
  ) => {
    const blocked = diagnosticRunBlock(
      id,
      target,
      requestInput,
      ['queued', 'running', 'cancel-requested'].includes(jobRef.current),
      window.innerWidth,
      options,
      getScenario(),
    );
    if (blocked) {
      notify(blocked);
      return blocked;
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
    updateReviewOnly(false);
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
    if (reviewOnlyRef.current) {
      const message = 'This result preview has no submitted Job to cancel.';
      notify(message);
      return message;
    }
    const blocked = diagnosticCancelBlock(
      requestRef.current?.id ?? '',
      jobRef.current,
      getScenario(),
      window.innerWidth,
    );
    if (blocked) {
      notify(blocked);
      return blocked;
    }
    updateJob('cancel-requested');
    setAutoAdvance(true);
    record('Audit', 'Synthetic diagnostic cancellation requested · job-3114');
    return null;
  };
  const reviewDiagnostic = (next: DiagnosticJobState) => {
    const currentScenario = getScenario();
    const blocked =
      window.innerWidth < 768
        ? 'Review diagnostic cases on tablet or desktop.'
        : diagnosticServiceBlock(currentScenario);
    if (blocked) {
      notify(blocked);
      return blocked;
    }
    if (next === 'not-started') {
      requestRef.current = null;
      setRequest(null);
      updateReviewOnly(false);
      setAutoAdvance(false);
      updateJob(next);
      go('diagnostic-run');
      return null;
    }
    if (!requestRef.current) {
      if (currentScenario === 'empty') {
        const message =
          'No diagnostic templates are available in the current catalog.';
        notify(message);
        return message;
      }
      try {
        saveRequest(
          captureDiagnosticRequest(selectedDiagnostic, scope, parameters),
        );
      } catch (error) {
        const message =
          error instanceof Error
            ? error.message
            : 'Review a valid bounded input first.';
        notify(message);
        return message;
      }
    }
    updateReviewOnly(true);
    setAutoAdvance(false);
    updateJob(next);
    go('diagnostic-run');
    return null;
  };
  const reviewOpenFlow = (next: OpenFlowReviewState) => {
    const blocked = openFlow.review(next);
    go('openflow-viewer');
    return blocked;
  };
  const openEvidence = (kind: 'event' | 'audit') => {
    if (reviewOnlyRef.current || !requestRef.current) {
      notify('A result preview does not create Event or Audit evidence.');
      return;
    }
    if (kind === 'event' && diagnosticActive(jobRef.current)) {
      notify(
        'The completion Event is pending. Inspect the request Audit instead.',
      );
      return;
    }
    notify(
      `Showing shared ${kind === 'event' ? 'Event' : 'Audit'} evidence · corr-DIAG-91C4`,
    );
    go('evidence');
  };
  const openObject = (target: string) => {
    const [kind, name] = target.split('/');
    if (kind === 'Bridge' && bridges.some((item) => item.name === name)) {
      setSelectedBridge(name);
      go('bridge-detail');
    } else if (kind === 'Port' && bonds.some((item) => item.name === name)) {
      setSelectedBond(name);
      go('bond-detail');
    } else if (kind === 'Port' && ports.some((item) => item.name === name))
      openPort(name);
    else
      notify(
        `No detail sample is available for ${target}. The captured target is preserved.`,
      );
  };
  useEffect(() => {
    if (
      !autoAdvance ||
      diagnosticServiceBlock(scenario) ||
      !['queued', 'running', 'cancel-requested'].includes(jobState)
    )
      return;
    const timer = window.setTimeout(
      () => {
        const currentScenario = handlers.current.getScenario();
        if (diagnosticServiceBlock(currentScenario)) return;
        const next =
          jobState === 'queued'
            ? 'running'
            : jobState === 'cancel-requested'
              ? 'cancelled'
              : ['degraded', 'provider-degraded'].includes(currentScenario)
                ? 'partial'
                : 'complete';
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
  }, [autoAdvance, jobState, request, scope, scenario]);

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
    reviewOnly,
    origin,
    clearOrigin: () => setOrigin(null),
    openFlow,
    openFlowState: openFlow.reviewCase,
    stageIntent,
    openDiagnostics,
    runDiagnostic,
    retryDiagnostic: () => {
      const original = requestRef.current;
      return original
        ? runDiagnostic(original.id, original.scope, original, 'valid')
        : 'No captured diagnostic request.';
    },
    cancelDiagnostic,
    reviewDiagnostic,
    reviewOpenFlow,
    openEvidence,
    openObject,
    busy: diagnosticActive(jobState),
  };
}

type P1Controller = ReturnType<typeof useP1Controller>;

export function P1Surface({
  view,
  mode,
  scenario,
  stageBlock,
  controller: p1,
  go,
  notify,
}: {
  view: P1View;
  mode: Mode;
  scenario: Scenario;
  stageBlock: string | null;
  controller: P1Controller;
  go: (view: View) => void;
  notify: (message: string) => void;
}) {
  if (p1SwitchingPages.some((page) => page === view))
    return (
      <P1SwitchingView
        view={view}
        mode={mode}
        scenario={scenario}
        stageBlock={stageBlock}
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
  if (view === 'openflow-viewer')
    return (
      <P1OpenFlowView
        mode={mode}
        controller={p1.openFlow}
        openObject={p1.openObject}
      />
    );
  if (view === 'diagnostics-hub' || view === 'diagnostic-run')
    return (
      <P1DiagnosticsView
        view={view}
        mode={mode}
        scenario={scenario}
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
        reviewOnly={p1.reviewOnly}
        busy={p1.busy}
        origin={p1.origin}
        clearOrigin={p1.clearOrigin}
        runDiagnostic={() => p1.runDiagnostic()}
        cancelDiagnostic={p1.cancelDiagnostic}
        retryDiagnostic={p1.retryDiagnostic}
        openEvidence={p1.openEvidence}
        openObject={() => p1.openObject(p1.request?.scope ?? p1.scope)}
        go={go}
        notify={notify}
      />
    );
  return null;
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
            ? p1.openFlow.status
            : view.startsWith('diagnostic')
              ? diagnosticJobLabels[p1.jobState]
              : 'Last known inventory'}
        </StatusBadge>
      </div>
      <p className="mt-3 text-sm">
        {view === 'openflow-viewer'
          ? p1.openFlow.snapshot
            ? `Bridge/${p1.openFlow.snapshot.query.bridge} · ${p1.openFlow.snapshot.rows.length} retained rows`
            : 'No readable snapshot'
          : view.startsWith('diagnostic')
            ? (p1.request?.scope ?? p1.scope)
            : p1.selectedBridge}
      </p>
      {view.startsWith('diagnostic') && p1.reviewOnly && (
        <p className="mt-3 text-sm text-muted-foreground">
          Result preview only · no new Event or Audit records.
        </p>
      )}
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
