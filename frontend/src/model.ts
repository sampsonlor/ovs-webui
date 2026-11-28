import { writable } from 'svelte/store';
import type {
  Accepted,
  Candidate,
  CaptureArtifact,
  CaptureTargets,
  InterfaceCounterDiagnosticCommand,
  InterfaceCounterDiagnosticResult,
  Login,
  LinkLACPDiagnosticCommand,
  LinkLACPDiagnosticResult,
  ManagementDebugCommand,
  ManagementServiceRestartCommand,
  Interface,
  InterfacePage,
  Port,
  PortsPage,
  ResourceRef,
  OpenFlowReadPage,
  RuntimeAccelerationEvidence,
  Session,
  SupportBundleMetadata,
  Transaction,
  Validation,
  VlanInput,
  Workspace,
} from '../../clients/typescript/public-v1.generated';
import { ResponseFence } from '../../clients/typescript/recovery.ts';
import { accelerationKind } from './acceleration.ts';
import { openFlowBridgePage, openFlowLocation, openFlowRoute, openFlowSnapshotPath, validOpenFlowPage } from './openflow.ts';
import { API, APIError } from './api.ts';
import type { Pending } from './api';
import { has } from './policy.ts';
import { interfaceListPath, readInterfaceQuery } from './interface-query.ts';
import type { InterfaceScope } from './interface-query';
import { telemetryMessage } from './telemetry.ts';
import { capabilitiesRoute, healthRoute } from './management-observation.ts';
import {
  canObserveDiagnostics,
  canSampleInterfaceCounters,
  diagnosticResultID,
  diagnosticResultRoute,
  diagnosticsRoute,
  diagnosticTargetQuery,
  diagnosticTargets,
  diagnosticTemplate,
  interfaceCounterJobPortID,
  interfaceCounterPath,
  interfaceCounterPortID,
  interfaceCounterResultRoute,
  interfaceCounterResultTarget,
  isLinkLACPJob,
  linkLACPPath,
  matchesInterfaceCounterResult,
  terminalDiagnosticJob,
} from './diagnostics.ts';
import {
  authorizedSupportScopes,
  supportBundleID,
  supportBundlePath,
  supportBundleResultRoute,
  supportBundleRoute,
  type SupportKind,
} from './support-bundles.ts';
import {
  canCancelCapture,
  captureElevationExpiry,
  captureInputsValid,
  capturePath,
  capturePermission,
  captureResultID,
  captureResultRoute,
  captureRoute,
  isCaptureJob,
  type CaptureProfile,
} from './packet-capture.ts';

export type Load<T> = {
  status: 'loading' | 'ready' | 'unavailable' | 'denied';
  value: T | null;
  error: string;
  received: number;
};
export const empty = <T>(): Load<T> => ({
  status: 'loading',
  value: null,
  error: '',
  received: 0,
});
function checkedCounterResult(
  load: Load<InterfaceCounterDiagnosticResult>,
  target: string,
): Load<InterfaceCounterDiagnosticResult> {
  if (load.status !== 'ready' || matchesInterfaceCounterResult(load.value, target))
    return load;
  return {
    ...load,
    status: 'unavailable',
    value: null,
    error: 'DIAGNOSTIC_RESULT_IDENTITY_MISMATCH',
  };
}
type RecordValue = Record<string, unknown>;
export type Model = {
  session: Session | null;
  booting: boolean;
  sessionReady: boolean;
  busy: boolean;
  message: string;
  pending: Pending | null;
  path: string;
  query: string;
  ports: Load<PortsPage>;
  port: Load<Port>;
  interfaces: Load<InterfacePage>;
  interface: Load<Interface>;
  interfaceFilter: string;
  interfaceLimit: number;
  interfaceBridgeID: string;
  interfaceNativeType: string | null;
  interfaceLinkState: string;
  workspace: Load<Workspace>;
  validation: Load<Validation>;
  transaction: Load<Transaction>;
  resource: Load<RecordValue>;
  linkLACPResult: Load<LinkLACPDiagnosticResult>;
  interfaceCounterResult: Load<InterfaceCounterDiagnosticResult>;
  supportBundle: Load<SupportBundleMetadata>;
  captureTargets: Load<CaptureTargets>;
  captureArtifact: Load<CaptureArtifact>;
  acceleration: Load<RuntimeAccelerationEvidence>;
  openFlowSnapshot: Load<OpenFlowReadPage>;
};
function route(value: string) {
  const [path, query = ''] = value.split('?', 2);
  return { path, query: query ? `?${query}` : '' };
}
function initial(path: string): Model {
  return {
    session: null,
    booting: true,
    sessionReady: false,
    busy: false,
    message: '',
    pending: null,
    ...route(path),
    ports: empty(),
    port: empty(),
    interfaces: empty(),
    interface: empty(),
    ...readInterfaceQuery(route(path).query),
    workspace: empty(),
    validation: empty(),
    transaction: empty(),
    resource: empty(),
    linkLACPResult: empty(),
    interfaceCounterResult: empty(),
    supportBundle: empty(),
    captureTargets: empty(),
    captureArtifact: empty(),
    acceleration: empty(),
    openFlowSnapshot: empty(),
  };
}
export function refPath(ref: ResourceRef | null): string | null {
  if (!ref || !/^[0-9a-f-]{36}$/.test(ref.id)) return null;
  const base: Record<string, string> = {
    candidate: '/changes/candidates',
    validation: '/changes/validations',
    transaction: '/changes/transactions',
    job: '/operations/jobs',
    event: '/operations/events',
    audit: '/operations/audit',
    port: '/ports',
    bridge: '/bridges',
    interface: '/interfaces',
    mirror: '/switching/mirrors',
    qos: '/switching/qos',
    statistic: '/visibility/statistics',
    neighbor: '/visibility/neighbors',
    lldp_neighbor: '/visibility/lldp',
    fdb_entry: '/visibility/fdb',
    endpoint: '/visibility/endpoints',
    capability: capabilitiesRoute,
  };
  return base[ref.kind] ? `${base[ref.kind]}/${ref.id}` : null;
}
export function errorText(error: unknown): string {
  if (
    error instanceof APIError &&
    error.problem.command_effect === 'not-started' &&
    error.problem.code === 'TRANSACTION_VERSION_CHANGED'
  )
    return 'The transaction changed before this decision was accepted. Review the refreshed evidence and choose again.';
  if (error instanceof APIError) {
    const code = error.problem.code;
    if (code && /^(?:INVALID_)?TELEMETRY_/.test(code))
      return telemetryMessage(code, error.message);
  }
  return error instanceof Error ? error.message : 'UNKNOWN_ERROR';
}
export class Controller {
  state: Model;
  readonly store;
  private fence = new ResponseFence();
  private context = 0;
  private reading = false;
  private queued = false;
  readonly api: API;
  constructor(api: API, path = '/ports') {
    this.api = api;
    this.state = initial(path === '/' ? '/ports' : path);
    this.store = writable(this.state);
  }
  private update(value: Partial<Model>) {
    this.state = { ...this.state, ...value };
    this.store.set(this.state);
  }
  private reset(session: Session | null) {
    this.context++;
    this.fence.reset();
    this.api.session = session;
    this.state = {
      ...initial(this.state.path + this.state.query),
      session,
      booting: false,
      sessionReady: !!session,
    };
    this.update({ pending: this.api.pending() });
  }
  async login(value: Login) {
    if (this.state.busy) return;
    this.update({ busy: true, message: '' });
    try {
      const session = await this.api.send<Session>('/sessions', 'POST', value);
      this.reset(session);
      await this.refresh();
    } catch (e) {
      this.update({ message: errorText(e), busy: false, booting: false });
    }
  }
  async logout() {
    const apiSession = this.api.session;
    this.context++;
    this.fence.reset();
    this.update({ busy: true, sessionReady: false });
    try {
      await this.api.send('/session', 'DELETE');
      this.reset(null);
    } catch (e) {
      this.api.session = apiSession;
      this.update({
        message: `Sign-out was not acknowledged: ${errorText(e)}`,
        busy: false,
      });
    }
  }
  async elevate(password: string) {
    if (this.state.busy) return;
    const context = this.context;
    this.update({ busy: true, message: '' });
    try {
      const session = await this.api.send<Session>(
        '/session/reauthentication',
        'POST',
        { password },
      );
      if (context === this.context) {
        this.api.session = session;
        this.update({
          session,
          message:
            'Identity verified. Authorization is checked again for each command.',
        });
      }
    } catch (e) {
      if (context === this.context) this.update({ message: errorText(e) });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
  go(path: string) {
    this.fence.reset();
    const target = route(path);
    this.update({
      ...target,
      ...(target.path === '/interfaces'
        ? readInterfaceQuery(target.query)
        : {}),
      port: empty(),
      interfaces: empty(),
      interface: empty(),
      validation: empty(),
      transaction: empty(),
      resource: empty(),
      linkLACPResult: empty(),
      interfaceCounterResult: empty(),
      supportBundle: empty(),
      captureTargets: empty(),
      captureArtifact: empty(),
      acceleration: empty(),
      openFlowSnapshot: empty(),
      message: '',
    });
    void this.refresh();
  }
  notify(message: string) {
    this.update({ message });
  }
  async refresh() {
    if (this.reading) {
      this.queued = true;
      return;
    }
    this.reading = true;
    const context = this.context;
    const ticket = this.fence.begin(this.state.path + this.state.query);
    const valid = () => context === this.context && this.fence.accepts(ticket);
    const path = this.state.path;
    const query = this.state.query;
    const selectedBridge = readInterfaceQuery(query).interfaceBridgeID;
    try {
      const session = await this.api.read<Session>('/session');
      if (!valid()) return;
      const old = this.state.session;
      if (
        old &&
        (old.principal_id !== session.principal_id ||
          JSON.stringify(old.effective_capabilities) !==
            JSON.stringify(session.effective_capabilities))
      ) {
        this.reset(session);
        this.queued = true;
        return;
      }
      this.api.session = session;
      this.update({
        session,
        sessionReady: true,
        booting: false,
        pending: this.api.pending(),
      });
      const read = async <T>(
        url: string,
        capability?: string,
      ): Promise<Load<T>> => {
        if (capability && !has(session, capability))
          return {
            status: 'denied',
            value: null,
            error: 'CAPABILITY_DENIED',
            received: 0,
          };
        try {
          return {
            status: 'ready',
            value: await this.api.read<T>(url),
            error: '',
            received: performance.now(),
          };
        } catch (e) {
          // Any auth failure invalidates the whole read batch, including delayed
          // successes from requests that started before revocation.
          if (e instanceof APIError && (e.status === 401 || e.status === 403))
            throw e;
          return {
            status: 'unavailable',
            value: null,
            error: errorText(e),
            received: performance.now(),
          };
        }
      };
      const workspace = await read<Workspace>('/workspace', 'workspace.read');
      const candidate = workspace.value?.candidate;
      let validationID = workspace.value?.latest_validation?.id;
      let transactionID = workspace.value?.latest_transaction?.id;
      const parts = path.split('/').filter(Boolean);
      if (parts[1] === 'validations') validationID = parts[2];
      if (parts[1] === 'transactions') transactionID = parts[2];
      const tasks: Promise<void>[] = [];
      const changes: Partial<Model> = { workspace };
      if (path.startsWith('/ports')) {
        if (parts[1])
          tasks.push(
            read<Port>(`/ports/${parts[1]}`, 'inventory.read').then((port) => {
              changes.port = port;
            }),
          );
        else
          tasks.push(
            read<PortsPage>(
              '/ports' +
                (this.portCursor
                  ? `?cursor=${encodeURIComponent(this.portCursor)}`
                  : ''),
              'inventory.read',
            ).then((ports) => {
              changes.ports = ports;
            }),
          );
      }
      if (parts[0] === 'interfaces') {
        if (parts[1])
          tasks.push(
            read<Interface>(`/interfaces/${parts[1]}`, 'inventory.read').then(
              (value) => {
                changes.interface = value;
              },
            ),
          );
        else {
          const query = new URLSearchParams(this.state.query);
          if (!query.has('limit')) query.set('limit', '25');
          tasks.push(
            read<InterfacePage>(
              `/interfaces?${query}`,
              !has(session, 'inventory.read')
                ? 'inventory.read'
                : query.has('native_type')
                  ? 'configuration.read'
                  : 'inventory.read',
            ).then((value) => {
              changes.interfaces = value;
            }),
          );
        }
      }
      if (validationID && has(session, 'configuration.validate'))
        tasks.push(
          read<Validation>(
            `/validations/${validationID}`,
            'configuration.validate',
          ).then((validation) => {
            changes.validation = validation;
          }),
        );
      else changes.validation = empty();
      if (transactionID)
        tasks.push(
          read<Transaction>(
            `/transactions/${transactionID}`,
            'configuration.read',
          ).then((transaction) => {
            changes.transaction = transaction;
          }),
        );
      else changes.transaction = empty();
      let resourceURL = '';
      let capability = '';
      if (path === openFlowRoute) {
        const location = openFlowLocation(query);
        capability = has(session, 'state.read') ? 'inventory.read' : 'state.read';
        if (location.kind === 'inventory') {
          resourceURL = openFlowBridgePage(location.filter, location.cursor);
        } else if (location.kind === 'snapshot') {
          tasks.push(read<OpenFlowReadPage>(openFlowSnapshotPath(location.id, location.offset), capability).then((snapshot) => {
            changes.openFlowSnapshot = snapshot.status === 'ready' && !validOpenFlowPage(snapshot.value, location.id, location.offset)
              ? { status: 'unavailable', value: null, error: 'OPENFLOW_VIEW_MISMATCH', received: snapshot.received }
              : snapshot;
          }));
        }
      } else if (accelerationKind(path)) {
        const required = has(session, 'state.read') ? 'inventory.read' : 'state.read';
        tasks.push(read<RuntimeAccelerationEvidence>('/runtime/acceleration', required).then((acceleration) => {
          changes.acceleration = acceleration;
        }));
      } else if (path === healthRoute) {
        resourceURL = '/health';
        capability = 'state.read';
      } else if (path === captureRoute) {
        const id = captureResultID(query);
        if (id) {
          const required = capturePermission(session)
            ? 'artifacts.read'
            : 'diagnostics.disruptive';
          tasks.push(
            read<CaptureArtifact>(`${capturePath}/${id}`, required).then(
              (captureArtifact) => {
                changes.captureArtifact = captureArtifact;
              },
            ),
          );
        } else
          tasks.push(
            read<CaptureTargets>(
              `${capturePath}/targets`,
              'diagnostics.disruptive',
            ).then((captureTargets) => {
              changes.captureTargets = captureTargets;
            }),
          );
      } else if (path === supportBundleRoute) {
        const id = supportBundleID(query);
        if (id) {
          const required = has(session, 'support_bundle.create')
            ? 'artifacts.read'
            : 'support_bundle.create';
          tasks.push(
            read<SupportBundleMetadata>(
              `${supportBundlePath}/${id}`,
              required,
            ).then((supportBundle) => {
              changes.supportBundle = supportBundle;
            }),
          );
        }
      } else if (path === diagnosticsRoute) {
        const template = diagnosticTemplate(query, session);
        const authorization =
          template === 'interface-counters' ? 'diagnostics.active' : 'diagnostics.read';
        capability = has(session, authorization) ? 'inventory.read' : authorization;
        const jobID = diagnosticResultID(query);
        if (jobID && template === 'interface-counters') {
          const target = interfaceCounterResultTarget(query);
          if (target && canSampleInterfaceCounters(session))
            tasks.push(
              read<InterfaceCounterDiagnosticResult>(
                `${interfaceCounterPath}/${jobID}`,
                'diagnostics.active',
              ).then((result) => {
                changes.interfaceCounterResult = checkedCounterResult(result, target);
              }),
            );
          else
            changes.interfaceCounterResult = {
              status: 'unavailable',
              value: null,
              error: 'DIAGNOSTIC_RESULT_TARGET_UNKNOWN',
              received: 0,
            };
        } else if (jobID)
          tasks.push(
            read<LinkLACPDiagnosticResult>(
              `${linkLACPPath}/${jobID}`,
              capability,
            ).then((result) => {
              changes.linkLACPResult = result;
            }),
          );
        else resourceURL = diagnosticTargetQuery(query, template).inventory;
      } else if (
        path === capabilitiesRoute ||
        path.startsWith(capabilitiesRoute + '/')
      ) {
        resourceURL =
          path === capabilitiesRoute
            ? '/capabilities' + query
            : '/capabilities/' +
              encodeURIComponent(path.slice(capabilitiesRoute.length + 1));
        capability = has(session, 'state.read')
          ? 'capabilities.read'
          : 'state.read';
      } else if (parts[0] === 'operations') {
        resourceURL =
          `/${parts.slice(1).join('/')}` + (parts.length === 2 ? query : '');
        capability =
          (
            {
              jobs: 'jobs.read',
              events: 'events.read',
              audit: 'audit.read',
            } as Record<string, string>
          )[parts[1]] ?? 'unavailable';
      } else if (
        path === '/switching/mirrors' ||
        path.startsWith('/switching/mirrors/')
      ) {
        resourceURL =
          path === '/switching/mirrors/new'
            ? '/inventory/topology?domain=mirror'
            : parts.length === 2
              ? '/mirrors' + query
              : `/mirrors/${encodeURIComponent(parts[2])}`;
        capability = 'configuration.read';
      } else if (
        path === '/switching/tunnels' ||
        path.startsWith('/switching/tunnels/')
      ) {
        resourceURL =
          path === '/switching/tunnels/new'
            ? '/inventory/topology'
            : parts.length === 2
              ? '/tunnels' + query
              : `/tunnels/${encodeURIComponent(parts[2])}`;
        capability = 'configuration.read';
      } else if (
        path === '/switching/qos' ||
        path.startsWith('/switching/qos/')
      ) {
        resourceURL =
          parts.length === 2
            ? '/qos' + query
            : `/qos/${encodeURIComponent(parts[2])}`;
        capability = 'configuration.read';
      } else if (
        path === '/visibility/statistics' ||
        path.startsWith('/visibility/statistics/')
      ) {
        const statisticQuery = new URLSearchParams(query);
        if (!statisticQuery.has('limit')) statisticQuery.set('limit', '25');
        resourceURL =
          parts.length === 2
            ? `/statistics?${statisticQuery}`
            : `/statistics/${encodeURIComponent(parts[2])}`;
        capability = has(session, 'inventory.read')
          ? 'state.read'
          : 'inventory.read';
      } else if (
        path === '/visibility/neighbors' ||
        path.startsWith('/visibility/neighbors/')
      ) {
        const neighborQuery = new URLSearchParams(query);
        if (!neighborQuery.has('limit')) neighborQuery.set('limit', '25');
        resourceURL =
          parts.length === 2
            ? `/neighbors?${neighborQuery}`
            : `/neighbors/${encodeURIComponent(parts[2])}`;
        capability = has(session, 'inventory.read')
          ? 'state.read'
          : 'inventory.read';
      } else if (
        path === '/visibility/lldp' ||
        path.startsWith('/visibility/lldp/')
      ) {
        const lldpQuery = new URLSearchParams(query);
        if (!lldpQuery.has('limit')) lldpQuery.set('limit', '25');
        resourceURL =
          parts.length === 2
            ? `/lldp?${lldpQuery}`
            : `/lldp/${encodeURIComponent(parts[2])}`;
        capability = has(session, 'inventory.read')
          ? 'state.read'
          : 'inventory.read';
      } else if (
        path === '/visibility/endpoints' ||
        path.startsWith('/visibility/endpoints/')
      ) {
        const endpointQuery = new URLSearchParams(query);
        if (parts.length === 2) {
          if (!endpointQuery.has('limit')) endpointQuery.set('limit', '25');
          resourceURL = '/endpoints?' + endpointQuery;
        } else {
          const ip = endpointQuery.get('ip');
          const detailQuery =
            ip === null ? '' : '?' + new URLSearchParams({ ip });
          resourceURL =
            '/endpoints/' + encodeURIComponent(parts[2]) + detailQuery;
        }
        capability = has(session, 'inventory.read')
          ? 'state.read'
          : 'inventory.read';
      } else if (
        path === '/visibility/fdb' ||
        path.startsWith('/visibility/fdb/')
      ) {
        const fdbQuery = new URLSearchParams(query);
        if (!fdbQuery.has('limit')) fdbQuery.set('limit', '25');
        resourceURL =
          parts.length === 2
            ? `/fdb?${fdbQuery}`
            : `/fdb/${encodeURIComponent(parts[2])}`;
        capability = has(session, 'inventory.read')
          ? 'state.read'
          : 'inventory.read';
      } else if (
        path === '/visibility/telemetry' ||
        path.startsWith('/visibility/telemetry/')
      ) {
        const telemetryQuery = new URLSearchParams(query);
        if (!telemetryQuery.has('limit')) telemetryQuery.set('limit', '25');
        resourceURL =
          parts.length === 2
            ? `/telemetry?${telemetryQuery}`
            : `/telemetry/${encodeURIComponent(parts[2])}`;
        capability = has(session, 'inventory.read')
          ? 'configuration.read'
          : 'inventory.read';
      } else if (path === '/switching/topology') {
        resourceURL = '/inventory/topology';
        capability = 'configuration.read';
      } else if (
        path === '/switching/spanning-tree' ||
        path.startsWith('/switching/spanning-tree/')
      ) {
        resourceURL =
          parts.length === 2
            ? '/inventory/spanning-tree' + query
            : `/bridges/${encodeURIComponent(parts[2])}/spanning-tree`;
        capability = 'inventory.read';
      } else if (
        path === '/switching/multicast' ||
        path.startsWith('/switching/multicast/')
      ) {
        resourceURL =
          parts.length === 2
            ? '/bridges' + query
            : `/bridges/${encodeURIComponent(parts[2])}`;
        capability = 'inventory.read';
      } else if (
        path === '/switching/isolation' ||
        path.startsWith('/switching/isolation/')
      ) {
        resourceURL =
          parts.length === 2
            ? '/ports' + query
            : `/ports/${encodeURIComponent(parts[2])}`;
        capability = 'inventory.read';
      } else if (parts[0] === 'bridges') {
        resourceURL = path + (parts.length === 1 ? query : '');
        capability = 'inventory.read';
      } else if (path === '/interfaces' && selectedBridge) {
        resourceURL = `/bridges/${encodeURIComponent(selectedBridge)}`;
        capability = 'inventory.read';
      } else if (path === '/changes/transactions') {
        resourceURL = '/transactions';
        capability = 'configuration.read';
      } else if (path === '/overview') resourceURL = '/runtime';
      if (resourceURL)
        tasks.push(
          read<RecordValue>(resourceURL, capability).then(async (resource) => {
            changes.resource = resource;
            if (
              parts[0] !== 'operations' ||
              parts[1] !== 'jobs' ||
              parts.length !== 3
            )
              return;
            const job = resource.value;
            changes.linkLACPResult = empty();
            changes.interfaceCounterResult = empty();
            changes.captureArtifact = empty();
            if (
              !valid() ||
              !job ||
              job.id !== parts[2] ||
              !terminalDiagnosticJob(job.state)
            )
              return;
            const target = canSampleInterfaceCounters(session)
              ? interfaceCounterJobPortID(job, session)
              : null;
            if (target) {
              changes.interfaceCounterResult = checkedCounterResult(
                await read<InterfaceCounterDiagnosticResult>(
                  `${interfaceCounterPath}/${job.id}`,
                  'diagnostics.active',
                ),
                target,
              );
            } else if (canObserveDiagnostics(session) && isLinkLACPJob(job, session)) {
              changes.linkLACPResult = await read<LinkLACPDiagnosticResult>(
                `${linkLACPPath}/${job.id}`,
                'diagnostics.read',
              );
            } else if (
              capturePermission(session) &&
              has(session, 'artifacts.read') &&
              isCaptureJob(job, session)
            ) {
              changes.captureArtifact = await read<CaptureArtifact>(
                `${capturePath}/${job.id}`,
                'artifacts.read',
              );
            }
          }),
        );
      await Promise.all(tasks);
      if (!valid()) return;
      this.update(changes);
      if (path === '/changes/candidate' && candidate)
        navigate(`/changes/candidates/${candidate.id}`, true);
    } catch (e) {
      if (!valid()) return;
      if (e instanceof APIError && e.status === 401) {
        this.reset(null);
        this.update({
          message:
            'Session expired or revoked. Sign in to resume server-owned resources.',
        });
      } else {
        // Clear protected values on failure rather than keeping revoked data in
        // page-local caches. Existing safety tasks continue on mgrd.
        this.update({
          booting: false,
          sessionReady: false,
          message: errorText(e),
          ports: empty(),
          port: empty(),
          interfaces: empty(),
          interface: empty(),
          workspace: empty(),
          validation: empty(),
          transaction: empty(),
          resource: empty(),
          linkLACPResult: empty(),
          interfaceCounterResult: empty(),
          supportBundle: empty(),
          captureTargets: empty(),
          captureArtifact: empty(),
          acceleration: empty(),
          openFlowSnapshot: empty(),
        });
      }
    } finally {
      this.reading = false;
      if (this.queued) {
        this.queued = false;
        void this.refresh();
      }
    }
  }
  private portCursor = '';
  interfacePage(
    filter = this.state.interfaceFilter,
    limit = this.state.interfaceLimit,
    cursor = '',
    scope: InterfaceScope = {
      bridgeID: this.state.interfaceBridgeID,
      nativeType: this.state.interfaceNativeType,
      linkState: this.state.interfaceLinkState,
    },
  ) {
    const path = interfaceListPath(filter, limit, cursor, scope);
    if (typeof window !== 'undefined') history.pushState(null, '', path);
    this.go(path);
  }
  page(cursor = '') {
    this.portCursor = cursor;
    this.update({ ports: empty() });
    void this.refresh();
  }
  async mutate(
    run: () => Promise<Candidate | Accepted>,
    after?: (value: Candidate | Accepted) => string | null,
  ) {
    if (this.state.busy || !this.state.sessionReady || this.state.pending)
      return;
    const context = this.context;
    this.update({ busy: true, message: '' });
    try {
      const result = await run();
      if (context !== this.context) return;
      // Reads started before the acknowledgement may still contain the previous
      // Candidate revision. Do not expose its command controls while the fresh
      // authoritative read is queued behind that older batch.
      this.fence.reset();
      this.update({
        workspace: empty(),
        validation: empty(),
        transaction: empty(),
        pending: this.api.pending(),
        message: 'Request acknowledged. Reading authoritative state…',
      });
      const path = after?.(result);
      if (path) navigate(path);
    } catch (e) {
      if (context === this.context)
        this.update({ pending: this.api.pending(), message: errorText(e) });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
  setManagementDebug(duration: ManagementDebugCommand['duration_seconds']) {
    if (
      this.state.path !== healthRoute ||
      !has(this.state.session, 'management.debug.write')
    )
      return;
    return this.mutate(() =>
      this.api.command<Accepted>('/health/debug', 'POST', 'management', {
        duration_seconds: duration,
      }),
    );
  }
  restartManagementService(
    service: ManagementServiceRestartCommand['service'],
  ) {
    if (
      this.state.path !== healthRoute ||
      !has(this.state.session, 'management.services.control')
    )
      return;
    return this.mutate(
      () =>
        this.api.command<Accepted>(
          '/health/service-restarts',
          'POST',
          'management',
          { service },
        ),
      (response) => '/operations/jobs/' + (response as Accepted).job_id,
    );
  }
  runLinkLACP(
    target: ResourceRef,
    duration: LinkLACPDiagnosticCommand['sample_seconds'],
    portLimit: number,
  ) {
    if (
      this.state.path !== diagnosticsRoute ||
      diagnosticResultID(this.state.query) ||
      diagnosticTemplate(this.state.query, this.state.session) !== 'link-lacp' ||
      !canObserveDiagnostics(this.state.session)
    )
      return;
    const { kind } = diagnosticTargetQuery(this.state.query);
    const available = diagnosticTargets(this.state.resource.value, kind);
    if (
      !available?.some(
        ({ ref }) => ref.id === target.id && ref.kind === target.kind,
      ) ||
      ![5, 10, 15].includes(duration) ||
      !Number.isInteger(portLimit) ||
      portLimit < 1 ||
      portLimit > 10
    )
      return;
    return this.mutate(
      () =>
        this.api.command<Accepted>(linkLACPPath, 'POST', 'management', {
          target_ref: { kind: target.kind, id: target.id },
          sample_seconds: duration,
          port_limit: portLimit,
        }),
      (response) =>
        this.state.path === diagnosticsRoute
          ? diagnosticResultRoute(
              (response as Accepted).job_id,
              this.state.session,
            )
          : null,
    );
  }
  runInterfaceCounters(
    target: ResourceRef,
    duration: InterfaceCounterDiagnosticCommand['sample_seconds'],
  ) {
    const { path, query, session, resource } = this.state;
    const portID = interfaceCounterPortID(target);
    if (
      path !== diagnosticsRoute ||
      diagnosticResultID(query) ||
      diagnosticTemplate(query, session) !== 'interface-counters' ||
      !canSampleInterfaceCounters(session) ||
      !portID ||
      resource.status !== 'ready' ||
      !diagnosticTargets(resource.value, 'port')?.some(
        ({ ref }) => ref.id === portID,
      ) ||
      ![5, 10, 15].includes(duration)
    )
      return;
    return this.mutate(
      () =>
        this.api.command<Accepted>(interfaceCounterPath, 'POST', 'management', {
          target_ref: { kind: 'port', id: portID },
          sample_seconds: duration,
        }),
      (response) =>
        this.state.path === path &&
        this.state.query === query &&
        canSampleInterfaceCounters(this.state.session)
          ? interfaceCounterResultRoute(
              (response as Accepted).job_id,
              portID,
              this.state.session,
            )
          : null,
    );
  }
  createSupportBundle(kinds: readonly SupportKind[]) {
    if (
      this.state.path !== supportBundleRoute ||
      supportBundleID(this.state.query) ||
      !has(this.state.session, 'support_bundle.create') ||
      !authorizedSupportScopes(kinds, this.state.session)
    )
      return;
    return this.mutate(
      () =>
        this.api.command<Accepted>(supportBundlePath, 'POST', 'management', {
          include_kinds: [...kinds],
          redact: true,
        }),
      (response) =>
        this.state.path === supportBundleRoute
          ? supportBundleResultRoute((response as Accepted).job_id)
          : null,
    );
  }
  startCapture(
    interfaceID: string,
    duration: number,
    bytes: number,
    profile: CaptureProfile,
    acknowledged: boolean,
    desktop: boolean,
  ) {
    const { session, path, query, captureTargets } = this.state;
    if (
      path !== captureRoute ||
      captureResultID(query) ||
      !desktop ||
      !acknowledged ||
      !capturePermission(session) ||
      !(captureElevationExpiry(session) > Date.now()) ||
      !captureInputsValid(interfaceID, duration, bytes, profile) ||
      captureTargets.status !== 'ready' ||
      captureTargets.value?.policy_state !== 'enabled' ||
      !captureTargets.value.targets.some((target) => target.id === interfaceID)
    )
      return;
    return this.mutate(
      () =>
        this.api.command<Accepted>(capturePath, 'POST', 'management', {
          interface_id: interfaceID,
          duration_seconds: duration,
          max_bytes: bytes,
          filter: profile,
        }),
      (response) =>
        this.state.path === captureRoute
          ? captureResultRoute(
              (response as Accepted).job_id,
              this.state.session,
            )
          : null,
    );
  }
  cancelCapture(id: string) {
    const { session, path, resource } = this.state;
    if (
      path !== `/operations/jobs/${id}` ||
      resource.value?.id !== id ||
      resource.status !== 'ready' ||
      !capturePermission(session) ||
      !canCancelCapture(resource.value, session)
    )
      return;
    return this.mutate(() =>
      this.api.command<Accepted>(
        `/jobs/${id}/cancellations`,
        'POST',
        'management',
        {},
      ),
    );
  }
  stage(port: Port, value: VlanInput) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    const existing = c.intents.find(
      (i) =>
        i.operation === 'port.vlan.set' &&
        i.object.management_id === port.management_id,
    );
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          {
            operation: 'stage',
            intents: [
              {
                intent_id: existing?.intent_id ?? crypto.randomUUID(),
                operation: 'port.vlan.set',
                object: {
                  management_id: port.management_id,
                  ovs_uuid: port.ovs_uuid,
                  instance_generation: port.instance_generation,
                  table: 'Port',
                },
                value,
              },
            ],
          },
          c.revision,
        ),
      (v) => `/changes/candidates/${(v as Candidate).id}`,
    );
  }
  stageMTU(item: Interface, requested: number | null) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    const existing = c.intents.find(
      (i) =>
        (i.operation === 'interface.mtu.set' ||
          i.operation === 'interface.mtu.clear') &&
        i.object.management_id === item.management_id,
    );
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          {
            operation: 'stage',
            intents: [
              {
                intent_id: existing?.intent_id ?? crypto.randomUUID(),
                operation:
                  requested === null
                    ? 'interface.mtu.clear'
                    : 'interface.mtu.set',
                object: {
                  management_id: item.management_id,
                  ovs_uuid: item.ovs_uuid,
                  instance_generation: item.instance_generation,
                  table: 'Interface',
                },
                ...(requested === null ? {} : { mtu_request: requested }),
              },
            ],
          },
          c.revision,
        ),
      (v) => `/changes/candidates/${(v as Candidate).id}`,
    );
  }
  stagePolicing(
    item: Interface,
    policing: { mode: 'disabled' | 'bandwidth' | 'packets'; rate: number },
  ) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    const existing = c.intents.find(
      (i) =>
        i.operation === 'interface.policing.set' &&
        i.object.management_id === item.management_id,
    );
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          {
            operation: 'stage',
            intents: [
              {
                intent_id: existing?.intent_id ?? crypto.randomUUID(),
                operation: 'interface.policing.set',
                object: {
                  management_id: item.management_id,
                  ovs_uuid: item.ovs_uuid,
                  instance_generation: item.instance_generation,
                  table: 'Interface',
                },
                policing,
              },
            ],
          },
          c.revision,
        ),
      (v) => `/changes/candidates/${(v as Candidate).id}`,
    );
  }
  stageSpanningTree(
    item: {
      management_id: string;
      ovs_uuid: string;
      instance_generation: string;
    },
    spanning_tree: {
      protocol: 'disabled' | 'stp' | 'rstp';
      parameters: Record<string, string | null>;
    },
    executable = false,
  ) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    const existing = c.intents.find(
      (i) =>
        i.operation ===
          (executable
            ? 'bridge.spanning-tree.configure'
            : 'bridge.spanning-tree.prepare') &&
        i.object.management_id === item.management_id,
    );
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          {
            operation: 'stage',
            intents: [
              {
                intent_id: existing?.intent_id ?? crypto.randomUUID(),
                operation: executable
                  ? 'bridge.spanning-tree.configure'
                  : 'bridge.spanning-tree.prepare',
                object: {
                  management_id: item.management_id,
                  ovs_uuid: item.ovs_uuid,
                  instance_generation: item.instance_generation,
                  table: 'Bridge',
                },
                spanning_tree,
              },
            ],
          },
          c.revision,
        ),
      (v) => `/changes/candidates/${(v as Candidate).id}`,
    );
  }
  candidateCommand(body: object) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    return this.mutate(() =>
      this.api.command<Candidate>(
        '/candidate',
        'PATCH',
        'workspace',
        body,
        c.revision,
      ),
    );
  }
  stageNativeIntent(intent: object) {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    return this.mutate(
      () =>
        this.api.command<Candidate>(
          '/candidate',
          'PATCH',
          'workspace',
          { operation: 'stage', intents: [intent] },
          c.revision,
        ),
      (value) => `/changes/candidates/${(value as Candidate).id}`,
    );
  }
  validate() {
    const c = this.state.workspace.value?.candidate;
    if (!c) return;
    return this.mutate(
      () =>
        this.api.command<Accepted>('/validations', 'POST', 'management', {
          candidate_id: c.id,
          candidate_revision: c.revision,
        }),
      (r) => refPath((r as Accepted).resource_ref),
    );
  }
  apply(reason: string) {
    const c = this.state.workspace.value?.candidate,
      v = this.state.validation.value;
    if (!c || !v) return;
    return this.mutate(
      () =>
        this.api.command<Accepted>('/transactions', 'POST', 'management', {
          candidate_id: c.id,
          candidate_revision: c.revision,
          validation_id: v.id,
          mode: 'safe-apply',
          reason,
        }),
      (r) => refPath((r as Accepted).resource_ref),
    );
  }
  decide(decision: 'confirm' | 'rollback') {
    const t = this.state.transaction.value;
    if (!t) return;
    return this.mutate(() =>
      this.api.command<Accepted>(
        `/transactions/${t.id}/decisions`,
        'POST',
        'management',
        { decision, expected_sequence: t.sequence },
      ),
    );
  }
  async recover() {
    if (this.state.busy || !this.state.sessionReady) return;
    const context = this.context;
    const pendingPath = this.state.pending?.path;
    this.update({ busy: true });
    try {
      const receipt = await this.api.recover();
      if (context !== this.context) return;
      this.update({
        pending: this.api.pending(),
        message: `Original receipt: ${receipt.state}. No command was resubmitted.`,
      });
      let path: string | null = null;
      if (pendingPath === supportBundlePath) {
        if (
          receipt.job_ref?.kind === 'job' &&
          has(this.state.session, 'support_bundle.create')
        )
          path = supportBundleResultRoute(receipt.job_ref.id);
      } else if (pendingPath === capturePath) {
        if (
          receipt.job_ref?.kind === 'job' &&
          capturePermission(this.state.session)
        )
          path = captureResultRoute(receipt.job_ref.id, this.state.session);
      } else if (pendingPath === interfaceCounterPath) {
        const portID = interfaceCounterPortID(receipt.resource_ref);
        if (
          ['accepted', 'completed'].includes(receipt.state) &&
          receipt.job_ref?.kind === 'job' &&
          portID &&
          canSampleInterfaceCounters(this.state.session)
        )
          path = interfaceCounterResultRoute(
            receipt.job_ref.id,
            portID,
            this.state.session,
          );
      } else if (pendingPath === linkLACPPath) {
        if (
          receipt.job_ref?.kind === 'job' &&
          canObserveDiagnostics(this.state.session)
        )
          path = diagnosticResultRoute(receipt.job_ref.id, this.state.session);
      } else path = refPath(receipt.resource_ref);
      if (path) navigate(path);
    } catch (e) {
      if (context === this.context)
        this.update({
          message: `Original outcome remains unknown: ${errorText(e)}. Refresh evidence; do not submit another command.`,
        });
    } finally {
      if (context === this.context) {
        this.update({ busy: false });
        await this.refresh();
      }
    }
  }
}

// Browser construction is lazy so authority/recovery logic can be tested without
// DOM globals or mock production services.
export let controller: Controller;
export function start(): Controller {
  controller = new Controller(
    new API(fetch.bind(window), localStorage),
    location.pathname + location.search,
  );
  return controller;
}
export function navigate(path: string, replace = false) {
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  controller.go(path);
}
