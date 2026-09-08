'use client';

import { useEffect, useRef, useState } from 'react';
import {
  accelerationAdmission,
  accelerationServiceBlock,
  assessAcceleration,
  captureAcceleration,
  filterAcceleration,
  projectAccelerationRecord,
  type AccelerationFamily,
  type AccelerationReviewCase,
  type AccelerationSnapshot,
} from '@/lib/acceleration-model';
import type { Scenario } from '@/lib/change-control';

type State = {
  snapshot: AccelerationSnapshot | null;
  reviewCase: AccelerationReviewCase;
  selected: AccelerationFamily;
  search: string;
  filter: string;
  tab: string;
  busy: boolean;
  failure: string | null;
};

export function useAccelerationController(
  scenario: Scenario,
  getScenario: () => Scenario,
  getGeneration: () => number,
  notify: (message: string) => void,
) {
  const [state, setState] = useState<State>({
    snapshot: null,
    reviewCase: 'normal',
    selected: 'dpdk',
    search: '',
    filter: 'all',
    tab: 'readiness',
    busy: false,
    failure: null,
  });
  const ref = useRef(state);
  const services = useRef({ getScenario, getGeneration, notify });
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const attempted = useRef(false);
  const token = useRef(0);
  const [now, setNow] = useState(0);
  const update = (patch: Partial<State>) => {
    ref.current = { ...ref.current, ...patch };
    setState(ref.current);
  };
  useEffect(() => {
    services.current = { getScenario, getGeneration, notify };
  });
  useEffect(() => {
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      clearInterval(clock);
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);
  useEffect(() => {
    if (accelerationServiceBlock(scenario)) {
      token.current++;
      if (timer.current) clearTimeout(timer.current);
      update({
        busy: false,
        ...(scenario === 'permission-denied'
          ? {
              snapshot: null,
              failure: 'Permission denied. Cached observations were cleared.',
            }
          : {}),
      });
    }
  }, [scenario]);
  const read = (reviewCase = ref.current.reviewCase) => {
    const block = accelerationAdmission(
      getScenario(),
      window.innerWidth,
      ref.current.busy,
    );
    if (block) {
      notify(block);
      return block;
    }
    attempted.current = true;
    const requestToken = ++token.current;
    const capturedGeneration = getGeneration();
    update({ busy: true, reviewCase });
    timer.current = setTimeout(() => {
      if (requestToken !== token.current) return;
      const current = services.current;
      const blocked = accelerationAdmission(
        current.getScenario(),
        window.innerWidth,
        false,
      );
      if (blocked) {
        update({
          busy: false,
          failure: blocked,
          ...(current.getScenario() === 'permission-denied'
            ? { snapshot: null }
            : {}),
        });
        current.notify(blocked);
        return;
      }
      const ended = Date.now();
      if (reviewCase === 'failed') {
        update({
          busy: false,
          failure:
            'Observation read failed. Retained observations are historical; retry does not change provider availability.',
        });
        current.notify('Observation read failed.');
        return;
      }
      const snapshot = captureAcceleration(
        current.getScenario() === 'empty' ? 'empty' : reviewCase,
        ended,
        capturedGeneration,
        current.getScenario(),
      );
      update({ snapshot, busy: false, failure: null });
      setNow(ended);
      current.notify(
        `Read ${snapshot.records.length} synthetic capability observations. Configuration was not changed.`,
      );
    }, 450);
    return null;
  };
  const snapshot = scenario === 'permission-denied' ? null : state.snapshot;
  const evidenceScenario = state.failure ? 'provider-unavailable' : scenario;
  const records = (scenario === 'empty' ? [] : (snapshot?.records ?? [])).map(
    (record) => projectAccelerationRecord(record, evidenceScenario),
  );
  const currentGeneration = getGeneration();
  const visible = filterAcceleration(
    records,
    state.search,
    state.filter,
    now,
    currentGeneration,
    evidenceScenario,
  );
  const selected =
    visible.find((record) => record.id === state.selected) ?? null;
  return {
    ...state,
    snapshot,
    records,
    visible,
    selected,
    now,
    generation: currentGeneration,
    scenario: evidenceScenario,
    serviceBlock: accelerationServiceBlock(scenario),
    status:
      accelerationServiceBlock(scenario) ??
      (state.busy
        ? 'Reading observations'
        : state.failure
          ? 'Read failed'
          : snapshot
            ? `${records.length} capabilities · ${snapshot.id}`
            : 'Not observed'),
    assessment: (record: (typeof records)[number]) =>
      assessAcceleration(record, now, currentGeneration, evidenceScenario),
    summary: () => {
      const liveScenario = getScenario();
      const current = ref.current;
      const visibleRecords =
        liveScenario === 'permission-denied' || liveScenario === 'empty'
          ? []
          : (current.snapshot?.records ?? []);
      return {
        status:
          accelerationServiceBlock(liveScenario) ??
          (current.busy
            ? 'Reading observations'
            : current.failure
              ? 'Read failed'
              : current.snapshot
                ? `${visibleRecords.length} capabilities`
                : 'Not observed'),
        reviewCase: current.reviewCase,
        capabilities: visibleRecords.map((record) => ({
          id: record.id,
          ...assessAcceleration(
            record,
            Date.now(),
            getGeneration(),
            current.failure ? 'provider-unavailable' : liveScenario,
          ),
        })),
      };
    },
    read: () => read(),
    ensureSnapshot: () => {
      if (
        !attempted.current &&
        !ref.current.snapshot &&
        window.innerWidth >= 768 &&
        !accelerationServiceBlock(getScenario())
      )
        read();
    },
    review: (reviewCase: AccelerationReviewCase) => read(reviewCase),
    setSearch: (search: string) => update({ search }),
    setFilter: (filter: string) => update({ filter }),
    clearFilters: () => update({ search: '', filter: 'all' }),
    select: (selected: AccelerationFamily) =>
      update({ selected, tab: 'readiness' }),
    setTab: (tab: string) => update({ tab }),
  };
}

export type AccelerationController = ReturnType<
  typeof useAccelerationController
>;
