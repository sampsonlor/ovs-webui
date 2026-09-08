'use client';

import { useEffect, useRef, useState } from 'react';
import type { ControlAction, ControlState } from '@/lib/change-control';
import {
  captureHealth,
  deriveHealth,
  healthEvents,
  healthReadBlock,
  preserveHealthSince,
  type HealthComponent,
  type HealthDomain,
  type HealthReviewCase,
  type HealthSignals,
  type HealthSnapshot,
} from '@/lib/health-model';

type State = {
  snapshot: HealthSnapshot | null;
  reviewCase: HealthReviewCase;
  busy: boolean;
  failure: string | null;
  domain: HealthDomain | 'all';
  search: string;
  selected: string | null;
};

// Mounted once at the prototype root. Consumers share a cache and never start polls.
export function useHealthController({
  control,
  getControl,
  signals,
  act,
  notify,
}: {
  control: ControlState;
  getControl: () => ControlState;
  signals: HealthSignals;
  act: (action: ControlAction) => ControlState;
  notify: (message: string) => void;
}) {
  const [state, setState] = useState<State>({
    snapshot: null,
    reviewCase: 'normal',
    busy: false,
    failure: null,
    domain: 'all',
    search: '',
    selected: null,
  });
  const ref = useRef(state);
  const services = useRef({ getControl, signals, act, notify });
  const previous = useRef<HealthComponent[]>([]);
  const [history, setHistory] = useState<HealthComponent[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const token = useRef(0);
  const [now, setNow] = useState(0);
  const update = (patch: Partial<State>) => {
    ref.current = { ...ref.current, ...patch };
    setState(ref.current);
  };
  useEffect(() => {
    services.current = { getControl, signals, act, notify };
  });
  const read = (reviewCase = ref.current.reviewCase, silent = false) => {
    const current = services.current;
    const blocked =
      healthReadBlock(current.getControl().scenario) ??
      (ref.current.busy ? 'A health read is already in progress.' : null);
    if (blocked) {
      if (!silent) current.notify(blocked);
      return blocked;
    }
    const requestToken = ++token.current;
    const generation = current.getControl().generation;
    update({ busy: true, reviewCase });
    timer.current = setTimeout(() => {
      if (requestToken !== token.current) return;
      const live = services.current;
      const block = healthReadBlock(live.getControl().scenario);
      if (block) {
        update({
          busy: false,
          failure: block,
          ...(live.getControl().scenario === 'permission-denied'
            ? { snapshot: null }
            : {}),
        });
        return;
      }
      if (reviewCase === 'failed') {
        update({
          busy: false,
          failure:
            'Health read failed. Retained observations are historical until a successful refresh.',
        });
        live.notify(
          'Health read failed. Shared transaction and Job resources are retained.',
        );
        return;
      }
      const ended = Date.now();
      update({
        snapshot: captureHealth(
          live.getControl().scenario === 'empty' ? 'empty' : reviewCase,
          ended,
          generation,
        ),
        busy: false,
        failure: null,
      });
      setNow(ended);
      if (!silent)
        live.notify('Health observations refreshed · synthetic sample.');
    }, 450);
    return null;
  };
  useEffect(() => {
    const initialRead = setTimeout(() => {
      setNow(Date.now());
      read('normal', true);
    }, 0);
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      clearTimeout(initialRead);
      if (timer.current) clearTimeout(timer.current);
      clearInterval(clock);
    };
  }, []);
  useEffect(() => {
    const block = healthReadBlock(control.scenario);
    if (block) {
      token.current++;
      if (timer.current) clearTimeout(timer.current);
      update({
        busy: false,
        failure: block,
        ...(control.scenario === 'permission-denied'
          ? { snapshot: null, selected: null }
          : {}),
      });
    }
  }, [control.scenario]);
  const derived = deriveHealth(
    state.snapshot,
    control,
    signals,
    now,
    !!state.failure,
  );
  const components = preserveHealthSince(history, derived.components, now);
  const incidents = derived.incidents.map((row) =>
    components.find((item) => item.id === row.id)!,
  );
  useEffect(() => {
    const identity = (rows: HealthComponent[]) =>
      JSON.stringify(
        rows.map((row) => [row.id, row.status, row.generation, row.related]),
      );
    if (identity(previous.current) === identity(components)) return;
    const eventDelivery = setTimeout(() => {
      const currentComponents =
        services.current.getControl().scenario === 'permission-denied'
          ? []
          : components;
      const events =
        services.current.getControl().scenario === 'permission-denied'
          ? []
          : healthEvents(previous.current, currentComponents, now);
      previous.current = currentComponents;
      setHistory(currentComponents);
      for (const entry of events)
        services.current.act({
          type: 'record-evidence',
          ...entry,
          now: entry.at,
        });
    }, 0);
    return () => clearTimeout(eventDelivery);
  }, [components, control.scenario, now]);
  const visible = components.filter(
    (row) =>
      (state.domain === 'all' || row.domain === state.domain) &&
      `${row.label} ${row.reason} ${row.related} ${row.source}`
        .toLowerCase()
        .includes(state.search.toLowerCase().trim()),
  );
  const selected =
    visible.find((row) => row.id === state.selected) ??
    visible.find((row) => row.status !== 'Healthy') ??
    visible[0] ??
    null;
  return {
    ...state,
    ...derived,
    components,
    incidents,
    visible,
    selected,
    now,
    generation: control.generation,
    serviceBlock: healthReadBlock(control.scenario),
    transaction: control.transaction,
    timeline:
      control.scenario === 'permission-denied'
        ? []
        : control.evidence
            .filter((entry) => entry.correlation.startsWith('corr-health-'))
            .slice(-12)
            .reverse(),
    read: () => read(),
    review: (reviewCase: HealthReviewCase) => read(reviewCase),
    setDomain: (domain: State['domain']) => update({ domain, selected: null }),
    setSearch: (search: string) => update({ search }),
    select: (selected: string) => update({ selected }),
    clearFilters: () => update({ domain: 'all', search: '', selected: null }),
    summary: () => {
      const current = services.current;
      const result = deriveHealth(
        ref.current.snapshot,
        current.getControl(),
        current.signals,
        Date.now(),
        !!ref.current.failure,
      );
      const stable = preserveHealthSince(
        previous.current,
        result.components,
        Date.now(),
      );
      return {
        ...result,
        components: stable,
        incidents: result.incidents.map((row) =>
          stable.find((item) => item.id === row.id)!,
        ),
        reviewCase: ref.current.reviewCase,
        busy: ref.current.busy,
        snapshotId:
          current.getControl().scenario === 'permission-denied'
            ? null
            : (ref.current.snapshot?.id ?? null),
      };
    },
  };
}

export type HealthController = ReturnType<typeof useHealthController>;
