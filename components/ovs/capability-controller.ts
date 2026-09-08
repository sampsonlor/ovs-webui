'use client';

import { useEffect, useRef, useState } from 'react';
import type { ControlAction, ControlState } from '@/lib/change-control';
import {
  capabilityCounts,
  capabilityReadBlock,
  capabilityReviewLabels,
  captureCapabilities,
  deriveCapabilities,
  filterCapabilities,
  type CapabilityId,
  type CapabilityReviewCase,
  type CapabilitySignals,
  type CapabilitySnapshot,
} from '@/lib/capability-model';
import { nativePolicyGates, nativeStageBlock } from '@/lib/native-capability';

type State = {
  snapshot: CapabilitySnapshot | null;
  busy: boolean;
  failure: string | null;
  selected: CapabilityId;
  search: string;
  filter: string;
  domain: string;
  reviewCase: CapabilityReviewCase;
  preparing: boolean;
  impactAccepted: boolean;
};
type Services = {
  getControl: () => ControlState;
  act: (action: ControlAction) => ControlState;
  notify: (message: string) => void;
  onStaged: () => void;
};
export function useCapabilityController({
  control,
  signals,
  configurationAvailable,
  ...services
}: Services & {
  control: ControlState;
  signals: CapabilitySignals;
  configurationAvailable: boolean;
}) {
  const [state, setState] = useState<State>({
    snapshot: null,
    busy: false,
    failure: null,
    selected: 'protected',
    search: '',
    filter: 'all',
    domain: 'all',
    reviewCase: 'normal',
    preparing: false,
    impactAccepted: false,
  });
  const ref = useRef(state);
  const serviceRef = useRef(services);
  const token = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [now, setNow] = useState(0);
  const update = (patch: Partial<State>) => {
    ref.current = { ...ref.current, ...patch };
    setState(ref.current);
  };
  useEffect(() => {
    serviceRef.current = services;
  });
  const read = (reviewCase = ref.current.reviewCase, quiet = false) => {
    const current = serviceRef.current;
    if (!Object.hasOwn(capabilityReviewLabels, reviewCase)) {
      const message =
        'Unknown capability sample. Choose a declared review case.';
      current.notify(message);
      return message;
    }
    const blocked =
      capabilityReadBlock(current.getControl().scenario) ??
      (window.innerWidth < 768
        ? 'Mobile retains incident evidence. Collect capability observations on tablet or desktop.'
        : ref.current.busy
          ? 'A registry read is already in progress.'
          : null);
    if (blocked) {
      if (!quiet) current.notify(blocked);
      return blocked;
    }
    const requestToken = ++token.current;
    const generation = current.getControl().generation;
    update({ busy: true, reviewCase, preparing: false, impactAccepted: false });
    timer.current = setTimeout(() => {
      if (requestToken !== token.current) return;
      const live = serviceRef.current;
      const control = live.getControl();
      const failure =
        capabilityReadBlock(control.scenario) ??
        (window.innerWidth < 768
          ? 'Registry collection stopped on mobile. Retained observations require a larger review surface.'
          : control.generation !== generation
            ? 'Generation changed during the registry read. Refresh before acting.'
            : reviewCase === 'failed'
              ? 'Registry read failed. Retained evidence is historical.'
              : null);
      if (failure) {
        update({ busy: false, failure });
        live.act({ type: 'observe-native-capability', proof: null });
        if (!quiet) live.notify(failure);
        return;
      }
      const ended = Date.now();
      const snapshot = captureCapabilities(
        control.scenario === 'empty' ? 'empty' : reviewCase,
        ended,
        generation,
        control.nativeEnabled ?? false,
      );
      live.act({ type: 'observe-native-capability', proof: snapshot.proof });
      update({ snapshot, busy: false, failure: null });
      setNow(ended);
      if (!quiet)
        live.notify('Capability evidence refreshed · synthetic registry.');
    }, 350);
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
      clearInterval(clock);
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);
  useEffect(() => {
    const failure = capabilityReadBlock(control.scenario);
    if (!failure) return;
    token.current++;
    if (timer.current) clearTimeout(timer.current);
    update({
      busy: false,
      failure,
      preparing: false,
      impactAccepted: false,
      ...(control.scenario === 'permission-denied' ? { snapshot: null } : {}),
    });
    serviceRef.current.act({ type: 'observe-native-capability', proof: null });
  }, [control.scenario]);
  const rows = deriveCapabilities(
    state.snapshot,
    control,
    signals,
    now,
    !!state.failure,
  );
  const selected = rows.find((row) => row.id === state.selected) ?? null;
  const unavailable = !configurationAvailable
    ? 'Native configuration is not connected to this persistence lab. Review this workflow in the synthetic prototype.'
    : null;
  const stageBlock =
    unavailable ??
    (state.busy
      ? 'Wait for the pending registry observation before preparing a change.'
      : null) ??
    nativeStageBlock(
      control.nativeCapability,
      control.scenario,
      control.generation,
      now,
      true,
      control.transaction.status,
    ) ??
    (control.candidate
      ? 'Review or discard the existing Candidate before preparing another intent.'
      : null);
  const gates = nativePolicyGates(
    control.nativeCapability,
    control.scenario,
    control.generation,
    now,
  );
  const admission = () => {
    const current = serviceRef.current.getControl();
    return (
      unavailable ??
      (ref.current.busy
        ? 'Wait for the pending registry observation before preparing a change.'
        : null) ??
      nativeStageBlock(
        current.nativeCapability,
        current.scenario,
        current.generation,
        Date.now(),
        window.innerWidth >= 1024,
        current.transaction.status,
      ) ??
      (current.candidate
        ? 'An existing Candidate must be reviewed first.'
        : null)
    );
  };
  return {
    ...state,
    now,
    rows,
    selected,
    counts: capabilityCounts(rows),
    gates,
    stageBlock,
    proof: control.nativeCapability,
    transaction: control.transaction,
    denied: control.scenario === 'permission-denied',
    visible: filterCapabilities(rows, state.search, state.filter, state.domain),
    readBlock: capabilityReadBlock(control.scenario),
    read: () => read(),
    review: (reviewCase: CapabilityReviewCase) => read(reviewCase),
    select: (selected: CapabilityId) =>
      update({ selected, preparing: false, impactAccepted: false }),
    setSearch: (search: string) => update({ search }),
    setFilter: (filter: string) => update({ filter }),
    setDomain: (domain: string) => update({ domain }),
    clearFilters: () => update({ search: '', filter: 'all', domain: 'all' }),
    prepare: () => {
      const blocked = admission();
      if (blocked) {
        serviceRef.current.notify(blocked);
        return;
      }
      update({ preparing: true, impactAccepted: false });
    },
    cancel: () => update({ preparing: false, impactAccepted: false }),
    acknowledge: (impactAccepted: boolean) => update({ impactAccepted }),
    stage: () => {
      const blocked = admission();
      if (blocked) {
        serviceRef.current.notify(blocked);
        return;
      }
      const next = serviceRef.current.act({
        type: 'stage-isolation',
        desktop: window.innerWidth >= 1024,
        impactAccepted: ref.current.impactAccepted,
        now: Date.now(),
      });
      if (!next.error) {
        update({ preparing: false, impactAccepted: false });
        serviceRef.current.onStaged();
      }
    },
  };
}
export type CapabilityController = ReturnType<typeof useCapabilityController>;
