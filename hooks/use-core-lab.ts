'use client';

import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { CoreHttpClient } from '@/lib/api/http-client';
import {
  WorkspaceController,
  type WorkspaceState,
} from '@/lib/api/workspace-controller';

type Session = {
  principal: string;
  label: string;
  nodeId: string;
  epoch: string;
  csrfToken: string;
  editable: boolean;
};
const initial: WorkspaceState = {
  phase: 'idle',
  snapshot: null,
  workspace: null,
  inventory: null,
  pendingRequestId: null,
  pendingValidation: null,
  validationJob: null,
  message: '',
  permissionError: false,
};
const emptySubscribe = () => () => undefined;

export function useCoreLab(enabled: boolean, pollValidation = true) {
  const [session, setSession] = useState<Session | null>(null);
  const [controller, setController] = useState<WorkspaceController | null>(
    null,
  );
  const [checking, setChecking] = useState(enabled);
  const [error, setError] = useState('');
  const requestVersion = useRef(0);
  const active = useRef<{
    session: Session | null;
    controller: WorkspaceController | null;
  }>({ session: null, controller: null });
  const state = useSyncExternalStore(
    controller?.subscribe ?? emptySubscribe,
    controller?.getSnapshot ?? (() => initial),
    () => initial,
  );

  const connect = async (principal?: string, logout = false) => {
    const version = ++requestVersion.current;
    setChecking(true);
    setError('');
    const old = active.current;
    // Clear private state before any sign-in/switch request or focus revalidation.
    old.controller?.dispose();
    active.current = { session: null, controller: null };
    setController(null);
    setSession(null);
    try {
      const response = await fetch('/__ovs_lab/session', {
        method: logout ? 'DELETE' : principal ? 'POST' : 'GET',
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        signal: AbortSignal.timeout(15_000),
        headers: {
          ...(principal ? { 'Content-Type': 'application/json' } : {}),
          ...(old.session ? { 'X-CSRF-Token': old.session.csrfToken } : {}),
        },
        ...(principal ? { body: JSON.stringify({ principal }) } : {}),
      });
      if (version !== requestVersion.current) return;
      if ((logout && response.ok) || response.status === 401) return;
      if (!response.ok)
        throw new Error(
          'Unable to open the local session. Refresh and choose the user again.',
        );
      const next: Session = await response.json();
      const { validateContract } =
        await import('@/lib/api/validator.generated.mjs');
      if (version !== requestVersion.current) return;
      if (
        !next ||
        !['alice', 'bob', 'observer'].includes(next.principal) ||
        !next.epoch ||
        !next.csrfToken ||
        !validateContract('Id', next.nodeId)
      )
        throw new Error('Invalid local session response.');
      const client = new CoreHttpClient({
        origin: window.location.origin,
        nodeId: next.nodeId,
        validate: validateContract,
        csrfToken: () => next.csrfToken,
        fetch: (url, options) => {
          const headers = new Headers(options?.headers);
          headers.set('X-OVS-Lab-Epoch', next.epoch);
          return fetch(url, { ...options, headers });
        },
      });
      const controller = new WorkspaceController(client);
      active.current = { session: next, controller };
      setSession(next);
      setController(controller);
      await controller.refresh();
    } catch (failure) {
      if (version === requestVersion.current)
        setError(
          failure instanceof Error
            ? failure.message
            : 'Local connection failed.',
        );
    } finally {
      if (version === requestVersion.current) setChecking(false);
    }
  };
  const connectRef = useRef(connect);
  useEffect(() => {
    connectRef.current = connect;
  });
  useEffect(() => {
    if (!enabled) return;
    void connectRef.current();
    const onFocus = () => {
      // Revalidate the Cookie-bound identity after another tab may have switched.
      void connectRef.current();
    };
    window.addEventListener('focus', onFocus);
    // The latest session controller owns cleanup; this ref is not a DOM node.
    const disposeSession = () => {
      requestVersion.current++;
      active.current.controller?.dispose();
    };
    return () => {
      disposeSession();
      window.removeEventListener('focus', onFocus);
    };
  }, [enabled]);
  const observe = async (scenario: string) => {
    const current = active.current;
    if (
      !current.session ||
      checking ||
      state.phase === 'writing' ||
      state.pendingRequestId
    )
      return;
    const version = ++requestVersion.current;
    setChecking(true);
    setError('');
    try {
      const response = await fetch('/__ovs_lab/observations', {
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        signal: AbortSignal.timeout(15_000),
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': current.session.csrfToken,
          'X-OVS-Lab-Epoch': current.session.epoch,
        },
        body: JSON.stringify({ scenario }),
      });
      if (!response.ok)
        throw new Error(
          'Stage a Candidate first and check the review user permissions.',
        );
      if (version === requestVersion.current)
        await current.controller?.refresh();
    } catch (failure) {
      if (version === requestVersion.current)
        setError(
          failure instanceof Error ? failure.message : 'Observation failed.',
        );
    } finally {
      if (version === requestVersion.current) setChecking(false);
    }
  };
  useEffect(() => {
    if (
      !pollValidation ||
      !controller ||
      checking ||
      state.phase !== 'ready' ||
      !state.workspace?.latestValidation
    )
      return;
    const status = state.workspace.latestValidation.status;
    // Server observations own completion and expiry; no browser countdown marks a result passed.
    const timer = setTimeout(
      () => void controller.pollValidation(),
      ['pending', 'running'].includes(status) ? 500 : 5000,
    );
    return () => clearTimeout(timer);
  }, [controller, checking, state, pollValidation]);
  return {
    session,
    controller,
    state,
    checking,
    error,
    connect,
    observe,
    canWrite: !checking && Boolean(controller?.canWrite()),
    canValidate: !checking && Boolean(controller?.canValidate()),
  };
}
