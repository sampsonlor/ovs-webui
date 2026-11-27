import type {
  StreamEnvelope,
  Subscription,
} from '../../clients/typescript/public-v1.generated';
import { StreamTracker } from '../../clients/typescript/recovery';
import { statisticsRoute } from './statistics';

const managementID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const sequence = /^[1-9][0-9]{0,19}$/;
const revision = /^[A-Za-z0-9_-]{1,128}$/;
const handshakeLimit = 8000;
const silenceLimit = 15000;
const retryDelay = 5000;
const refreshDelay = 120;

export function statisticDetailID(path: string): string | null {
  if (!path.startsWith(statisticsRoute + '/')) return null;
  const id = path.slice(statisticsRoute.length + 1);
  return managementID.test(id) ? id : null;
}

function statisticEnvelope(data: unknown, id: string): StreamEnvelope | null {
  if (typeof data !== 'string' || data.length > 8192) return null;
  let frame: Partial<StreamEnvelope> | null;
  try {
    frame = JSON.parse(data);
  } catch {
    return null;
  }
  if (
    !frame ||
    typeof frame.stream_id !== 'string' ||
    !managementID.test(frame.stream_id) ||
    typeof frame.sequence !== 'string' ||
    !sequence.test(frame.sequence) ||
    typeof frame.observed_at !== 'string' ||
    !Number.isFinite(Date.parse(frame.observed_at)) ||
    (frame.correlation_id !== null &&
      (typeof frame.correlation_id !== 'string' || !managementID.test(frame.correlation_id)))
  )
    return null;
  switch (frame.type) {
    case 'heartbeat':
    case 'resync.required':
      if (frame.resource_ref !== null || frame.resource_version !== null) return null;
      break;
    case 'resource.changed':
    case 'state.coalesced':
      if (
        frame.resource_ref?.kind !== 'statistic' ||
        frame.resource_ref.id !== id ||
        typeof frame.resource_version !== 'string' ||
        !revision.test(frame.resource_version)
      )
        return null;
      break;
    default:
      return null;
  }
  return frame as StreamEnvelope;
}

export function watchStatisticNotifications(
  id: string,
  active: () => boolean,
  refresh: () => void,
  setReady: (ready: boolean) => void,
): () => void {
  const url = new URL('/api/v1/stream', window.location.origin);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  const subscription: Subscription = {
    resources: [{ kind: 'statistic', id }],
    cursor: null,
  };
  let socket: WebSocket | null = null;
  let disposed = false;
  let retry: number | undefined;
  let deadline: number | undefined;
  let refreshTimer: number | undefined;

  function scheduleRefresh() {
    if (refreshTimer !== undefined || disposed || !active()) return;
    refreshTimer = window.setTimeout(() => {
      refreshTimer = undefined;
      if (!disposed && active()) refresh();
    }, refreshDelay);
  }
  function close() {
    window.clearTimeout(deadline);
    const previous = socket;
    socket = null;
    setReady(false);
    if (previous && previous.readyState < WebSocket.CLOSING) previous.close();
  }
  function disconnected() {
    close();
    if (!disposed && active() && retry === undefined) {
      retry = window.setTimeout(() => {
        retry = undefined;
        connect();
      }, retryDelay);
    }
  }
  function connect() {
    if (disposed || !active()) return;
    let next: WebSocket;
    try {
      next = new WebSocket(url, 'ovs.v1');
    } catch {
      disconnected();
      return;
    }
    socket = next;
    let subscribed = false;
    const tracker = new StreamTracker();
    const current = () => socket === next && !disposed && active();
    const armDeadline = (limit: number) => {
      window.clearTimeout(deadline);
      deadline = window.setTimeout(() => {
        if (current()) disconnected();
      }, limit);
    };
    armDeadline(handshakeLimit);
    next.addEventListener('open', () => {
      if (!current()) return;
      if (next.protocol !== 'ovs.v1') {
        disconnected();
        return;
      }
      next.send(JSON.stringify(subscription));
    });
    next.addEventListener('message', (event) => {
      if (!current()) return;
      const frame = statisticEnvelope(event.data, id);
      if (
        !frame ||
        (!subscribed && (frame.type !== 'resync.required' || frame.sequence !== '1'))
      ) {
        disconnected();
        return;
      }
      armDeadline(silenceLimit);
      subscribed = true;
      const action = tracker.accept(frame);
      if (action === 'resync') setReady(false);
      if (action === 'hint') setReady(true);
      // A heartbeat also schedules a read so that browsing authorization is
      // rechecked against the current cookie even when counters are idle.
      if (action !== 'ignore' || frame.type === 'heartbeat') scheduleRefresh();
    });
    const onDisconnect = () => {
      if (current()) disconnected();
    };
    next.addEventListener('close', onDisconnect);
    next.addEventListener('error', onDisconnect);
  }

  connect();
  return () => {
    disposed = true;
    window.clearTimeout(retry);
    window.clearTimeout(refreshTimer);
    close();
  };
}
