'use client';

import { useEffect, useRef, useState } from 'react';
import {
  openFlowReviewLabels,
  type OpenFlowReviewState,
} from '@/app/prototype-model';
import { scenarioLabels, type Scenario } from '@/lib/change-control';
import {
  collectFlowSnapshot,
  defaultFlowQuery,
  flowAdmission,
  flowFreshness,
  flowServiceBlock,
  openFlowLimits,
  serializeFlowSnapshot,
  type FlowFailure,
  type FlowQuery,
  type FlowSnapshot,
} from '@/lib/openflow-model';

type State = {
  draft: FlowQuery;
  snapshot: FlowSnapshot | null;
  reviewCase: OpenFlowReviewState;
  failure: FlowFailure | null;
  busy: boolean;
  page: number;
  selectedId: string | null;
};

export function useOpenFlowController(
  scenario: Scenario,
  getScenario: () => Scenario,
  notify: (message: string) => void,
) {
  const [state, setState] = useState<State>({
    draft: { ...defaultFlowQuery },
    snapshot: null,
    reviewCase: 'fresh',
    failure: null,
    busy: false,
    page: 1,
    selectedId: null,
  });
  const ref = useRef(state);
  const update = (patch: Partial<State>) => {
    ref.current = { ...ref.current, ...patch };
    setState(ref.current);
  };
  const services = useRef({ getScenario, notify });
  useEffect(() => {
    services.current = { getScenario, notify };
  });
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const generation = useRef(0);
  const [now, setNow] = useState(0);
  const clearTimers = () => {
    timers.current.forEach(clearTimeout);
    timers.current = [];
  };
  useEffect(() => {
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      clearInterval(clock);
      clearTimers();
    };
  }, []);
  useEffect(() => {
    if (flowServiceBlock(scenario)) {
      generation.current++;
      clearTimers();
      ref.current = {
        ...ref.current,
        busy: false,
        ...(scenario === 'permission-denied'
          ? {
              snapshot: null,
              failure: 'permission-denied',
              selectedId: null,
              page: 1,
            }
          : {}),
      };
      setState(ref.current);
    }
  }, [scenario]);

  const collect = (
    query = ref.current.draft,
    reviewCase = ref.current.reviewCase,
  ) => {
    const blocked = flowAdmission(
      query,
      getScenario(),
      window.innerWidth,
      ref.current.busy,
    );
    if (blocked) {
      notify(blocked);
      return blocked;
    }
    const captured = { ...query };
    const token = ++generation.current;
    const started = Date.now();
    update({ busy: true, reviewCase, failure: null });
    const complete = (timeout = false) => {
      if (token !== generation.current) return;
      clearTimers();
      const currentScenario = services.current.getScenario();
      const blockedNow = flowServiceBlock(currentScenario);
      if (blockedNow || window.innerWidth < 768) {
        update({
          busy: false,
          ...(currentScenario === 'permission-denied'
            ? { snapshot: null, failure: 'permission-denied' }
            : {}),
        });
        services.current.notify(
          blockedNow ??
            'Collection stopped after switching to the incident companion.',
        );
        return;
      }
      const ended = Date.now();
      const result = collectFlowSnapshot(
        captured,
        timeout ? 'query-timeout' : reviewCase,
        ended,
        ended - started,
        ['degraded', 'provider-degraded'].includes(currentScenario),
      );
      update({ ...result, busy: false, page: 1, selectedId: null });
      setNow(ended);
      services.current.notify(
        result.failure
          ? `OpenFlow collection · ${result.failure}. No snapshot was returned.`
          : `Captured ${result.snapshot!.rows.length} synthetic rows for ${captured.bridge}.`,
      );
    };
    timers.current = [
      setTimeout(() => complete(), 450),
      setTimeout(() => complete(true), openFlowLimits.timeoutMs),
    ];
    return null;
  };
  const visibleSnapshot =
    scenario === 'permission-denied' || state.failure === 'permission-denied'
      ? null
      : state.snapshot;
  const degraded = ['degraded', 'provider-degraded'].includes(scenario);
  const status =
    (flowServiceBlock(scenario) ? scenarioLabels[scenario] : null) ??
    (state.busy
      ? 'Collecting'
      : ((state.failure ? openFlowReviewLabels[state.failure] : null) ??
        (visibleSnapshot
          ? `${flowFreshness(visibleSnapshot, now)} · ${visibleSnapshot.truncation.length || visibleSnapshot.providerDegraded || degraded ? 'Partial coverage' : visibleSnapshot.rows.length ? 'Query complete' : '0 rows'}`
          : 'Not collected')));
  return {
    ...state,
    snapshot: visibleSnapshot,
    now,
    status,
    degraded,
    serviceBlock: flowServiceBlock(scenario),
    setDraft: (patch: Partial<FlowQuery>) => {
      if (!ref.current.busy)
        update({ draft: { ...ref.current.draft, ...patch } });
    },
    setPage: (page: number) => update({ page, selectedId: null }),
    select: (selectedId: string) => update({ selectedId }),
    collect: () => collect(),
    refresh: () => collect(ref.current.snapshot?.query ?? ref.current.draft),
    review: (reviewCase: OpenFlowReviewState) =>
      collect(ref.current.draft, reviewCase),
    exportSnapshot: () => {
      const current = ref.current;
      const currentScenario = getScenario();
      if (
        currentScenario === 'permission-denied' ||
        current.failure ||
        !current.snapshot ||
        current.busy
      ) {
        notify('A permitted, completed snapshot is required for export.');
        return;
      }
      const payload = serializeFlowSnapshot(
        current.snapshot,
        Date.now(),
        ['degraded', 'provider-degraded'].includes(currentScenario),
        !!flowServiceBlock(currentScenario),
      );
      const url = URL.createObjectURL(
        new Blob([payload], { type: 'application/json' }),
      );
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = `${current.snapshot.id}.json`;
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
      notify(
        `Snapshot export prepared · ${current.snapshot.query.bridge} · ${current.snapshot.rows.length} rows. Freshness and coverage are included.`,
      );
    },
  };
}

export type OpenFlowController = ReturnType<typeof useOpenFlowController>;
