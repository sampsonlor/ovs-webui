import type {
  DiagnosticJobState,
  DiagnosticParameters,
  DiagnosticRequest,
} from '../app/prototype-model';
import { inventoryPorts } from './inventory-model.ts';

const storageMembers = inventoryPorts.find(
  (port) => port.name === 'bond-storage',
)!.interfaces;

export type DiagnosticDefinition = {
  id: string;
  name: string;
  category: 'Network' | 'OVS' | 'OpenFlow' | 'Host' | 'Acceleration';
  description: string;
  permission: string;
  impact: string;
  availability: 'Available' | 'Permission denied' | 'Provider unavailable';
  recommended: boolean;
  safeCancel: boolean;
  scopes: Array<'Port' | 'Bridge'>;
  timeout: number;
  sampleBudgets: Array<5 | 10 | 15>;
  textLineLimit: number;
};

// Declared prototype capabilities, not live permission or provider decisions.
export const diagnostics: DiagnosticDefinition[] = [
  {
    id: 'diag.net.link-lacp',
    name: 'Interface link & LACP snapshot',
    category: 'Network',
    description:
      'Read a bounded link and LACP snapshot for one Port or a Bridge sample.',
    permission: 'diagnostic.run.network',
    impact: 'Read-only · low sampling cost',
    availability: 'Available',
    recommended: true,
    safeCancel: true,
    scopes: ['Port', 'Bridge'],
    timeout: 20,
    sampleBudgets: [5, 10, 15],
    textLineLimit: 200,
  },
  {
    id: 'diag.ovs.datapath-trace',
    name: 'Bounded datapath packet trace',
    category: 'OVS',
    description:
      'Inspect a predefined synthetic packet trace against one Bridge.',
    permission: 'diagnostic.run.ovs',
    impact: 'Read-only · one synthetic packet',
    availability: 'Available',
    recommended: true,
    safeCancel: true,
    scopes: ['Bridge'],
    timeout: 20,
    sampleBudgets: [5, 10, 15],
    textLineLimit: 300,
  },
  {
    id: 'diag.openflow.collection',
    name: 'OpenFlow collection health',
    category: 'OpenFlow',
    description:
      'Read collection freshness, authority and coverage for one Bridge.',
    permission: 'diagnostic.run.openflow',
    impact: 'Read-only metadata query',
    availability: 'Available',
    recommended: true,
    safeCancel: false,
    scopes: ['Bridge'],
    timeout: 10,
    sampleBudgets: [5, 10],
    textLineLimit: 0,
  },
  {
    id: 'diag.host.interface-counters',
    name: 'Host interface counter sample',
    category: 'Host',
    description:
      'Capture bounded counter deltas for selected member Interfaces.',
    permission: 'diagnostic.run.host',
    impact: 'Read-only · capped at 4 Interfaces',
    availability: 'Permission denied',
    recommended: false,
    safeCancel: true,
    scopes: ['Port'],
    timeout: 20,
    sampleBudgets: [5, 10, 15],
    textLineLimit: 200,
  },
  {
    id: 'diag.acceleration.provider',
    name: 'Acceleration provider snapshot',
    category: 'Acceleration',
    description:
      'Read declared acceleration prerequisites and provider freshness.',
    permission: 'diagnostic.run.acceleration',
    impact: 'Read-only provider query',
    availability: 'Provider unavailable',
    recommended: false,
    safeCancel: false,
    scopes: ['Bridge'],
    timeout: 20,
    sampleBudgets: [5, 10, 15],
    textLineLimit: 0,
  },
];

export const diagnosticOutputBytes = 64 * 1024;
export const diagnosticJobId = 'job-3114';
export const diagnosticCorrelation = 'corr-DIAG-91C4';

export function diagnosticDefinition(id: string) {
  return diagnostics.find((item) => item.id === id);
}

export function diagnosticScopeError(id: string, scope: string): string | null {
  if (!/^(Port|Bridge)\/[a-zA-Z0-9_.-]{1,63}$/.test(scope))
    return 'Choose one explicit Port or Bridge scope.';
  const definition = diagnosticDefinition(id);
  if (!definition?.scopes.some((kind) => scope.startsWith(kind + '/')))
    return `This template requires one ${definition?.scopes.join(' or ') ?? 'supported object'}.`;
  return null;
}

export function diagnosticParameterErrors(
  id: string,
  parameters: DiagnosticParameters,
) {
  const definition = diagnosticDefinition(id);
  return {
    sample: !definition?.sampleBudgets.includes(parameters.sampleSeconds)
      ? `Choose a supported sampling budget: ${definition?.sampleBudgets.join(', ') ?? 'no available'} seconds.`
      : null,
    detail: !['structured', 'bounded'].includes(parameters.detail)
      ? 'Choose structured summary or bounded text.'
      : parameters.detail === 'bounded' && !definition?.textLineLimit
        ? 'This template supplies structured output only.'
        : null,
  };
}

export function diagnosticServiceBlock(scenario: string): string | null {
  if (scenario === 'permission-denied')
    return 'Diagnostic permission is unavailable.';
  if (
    ['provider-unavailable', 'network-loss', 'error', 'loading'].includes(
      scenario,
    )
  )
    return 'The diagnostic service is unavailable. Restore the connection before starting or cancelling a Job.';
  return null;
}

export function diagnosticActive(state: DiagnosticJobState) {
  return ['queued', 'running', 'cancel-requested'].includes(state);
}

export function diagnosticCancelBlock(
  id: string,
  state: DiagnosticJobState,
  scenario: string,
  width: number,
): string | null {
  if (width < 768) return 'Handle diagnostic Jobs on tablet or desktop.';
  const blocked = diagnosticServiceBlock(scenario);
  if (blocked) return blocked;
  if (!['queued', 'running'].includes(state))
    return 'No running Job is waiting for a cancellation request.';
  if (!diagnosticDefinition(id)?.safeCancel)
    return 'This atomic diagnostic does not support safe cancellation.';
  return null;
}

export function diagnosticRetryable(state: DiagnosticJobState) {
  return [
    'cancelled',
    'failed',
    'expired',
    'unavailable',
    'no-data',
    'provider-unavailable',
    'command-failed',
  ].includes(state);
}

type DiagnosticTone =
  | 'neutral'
  | 'info'
  | 'success'
  | 'warning'
  | 'danger'
  | 'uncertain';

export function diagnosticJobTone(state: DiagnosticJobState): DiagnosticTone {
  if (state === 'no-finding') return 'success';
  if (['failed', 'command-failed'].includes(state)) return 'danger';
  if (
    [
      'partial',
      'truncated',
      'cancel-requested',
      'evidence-unavailable',
    ].includes(state)
  )
    return 'warning';
  if (['no-data', 'unavailable', 'provider-unavailable'].includes(state))
    return 'uncertain';
  if (diagnosticActive(state) || state === 'complete') return 'info';
  return 'neutral';
}

export function diagnosticResult(
  request: DiagnosticRequest,
  state: DiagnosticJobState,
) {
  const definition = diagnosticDefinition(request.id);
  const storage =
    request.id === 'diag.net.link-lacp' &&
    request.scope === 'Port/bond-storage';
  const outputAvailable = [
    'complete',
    'partial',
    'truncated',
    'no-finding',
    'evidence-unavailable',
  ].includes(state);
  const result = {
    title: storage ? 'Member link fault detected' : 'Bounded snapshot retained',
    body: storage
      ? 'One member has no carrier. The Bond is available through the other member, with reduced redundancy.'
      : 'The synthetic collection completed for this object. Review the captured scope before preparing a configuration change.',
    finding: storage
      ? `${storageMembers[1]} carrier down · ${storageMembers[0]} active`
      : `Captured ${definition?.category ?? 'provider'} snapshot · ${request.scope}`,
    coverage: `One explicit target · ${request.sampleSeconds}s budget`,
    next: 'Inspect the related object and correlated evidence before staging intent.',
    tone: (storage ? 'warning' : 'info') as DiagnosticTone,
  };
  if (state === 'no-finding')
    Object.assign(result, {
      title: 'No finding in sampled scope',
      body: 'The bounded checks completed without detecting a fault. This does not establish the health of unsampled objects.',
      finding: 'No fault detected in this sample',
      tone: 'success',
      next: 'Return to the incident and review any remaining evidence domains.',
    });
  else if (state === 'partial')
    Object.assign(result, {
      title: 'Partial result',
      body: storage
        ? 'Carrier state was captured, but one counter sample is missing. The omitted evidence remains unknown.'
        : 'One provider sample was captured; additional evidence is missing. Coverage remains incomplete.',
      finding: storage
        ? `${storageMembers[1]} carrier down · counters unknown`
        : `Partial ${definition?.category ?? 'provider'} snapshot · ${request.scope}`,
      coverage: 'Incomplete evidence for the requested target',
      tone: 'warning',
      next: 'Check provider freshness before drawing a wider conclusion.',
    });
  else if (state === 'truncated')
    Object.assign(result, {
      title: 'Result retained with truncation',
      body: definition?.textLineLimit
        ? 'The structured finding is retained. Provider text reached its hard limit; the excerpt is not the complete output.'
        : 'The structured collection reached its output limit. Omitted records were not inspected.',
      coverage: 'Bounded output · omitted evidence remains unreviewed',
      tone: 'warning',
      next: 'Review the retained finding; narrow the next collection if more evidence is needed.',
    });
  else if (state === 'evidence-unavailable')
    Object.assign(result, {
      title: 'Result retained · Event link unavailable',
      body: 'The result is available, but its Event correlation was not acknowledged. Audit and result availability are separate.',
      tone: 'warning',
      next: 'Preserve the result and inspect Audit while the Event link is unavailable.',
    });
  else if (state === 'no-data')
    Object.assign(result, {
      title: 'No data',
      body: 'The provider replied successfully with zero samples for this request.',
      finding: 'Zero samples · health unknown',
      coverage: 'No sample coverage',
      tone: 'uncertain',
      next: 'Verify the provider and sampling interval before retrying the same bounded input.',
    });
  else if (state === 'provider-unavailable')
    Object.assign(result, {
      title: 'Provider unavailable',
      body: 'The provider could not supply observations for this operation.',
      finding: 'No authoritative observations · health unknown',
      coverage: 'Provider evidence unavailable',
      tone: 'uncertain',
      next: 'Restore the provider, then retry the same bounded input.',
    });
  else if (state === 'unavailable')
    Object.assign(result, {
      title: 'Result unavailable',
      body: 'Job metadata is retained, but its result cannot be retrieved. This does not prove that execution failed.',
      finding: 'Result cannot be read · outcome unverified',
      coverage: 'Output unavailable',
      tone: 'uncertain',
      next: 'Inspect Job evidence before deciding whether another read-only collection is needed.',
    });
  else if (state === 'failed' || state === 'command-failed')
    Object.assign(result, {
      title:
        state === 'failed'
          ? 'Diagnostic Job failed'
          : 'Predefined operation failed',
      body: 'Execution ended before a valid structured result was produced.',
      finding: 'Execution failure · no health verdict',
      coverage: 'No valid result',
      tone: 'danger',
      next: 'Inspect failure evidence before retrying the same bounded input.',
    });
  else if (state === 'expired')
    Object.assign(result, {
      title: 'Result expired',
      body: 'Output is outside its retention period. Job metadata and Audit can still be reviewed.',
      finding: 'Output no longer retained',
      coverage: 'Historical metadata only',
      tone: 'neutral',
      next: 'Start another bounded collection if current evidence is required.',
    });
  else if (state === 'cancelled')
    Object.assign(result, {
      title: 'Cancelled at a safe checkpoint',
      body: 'The provider stopped without publishing a diagnostic finding.',
      finding: 'No result · cancellation is not a health verdict',
      coverage: 'Collection stopped',
      tone: 'neutral',
      next: 'Retry the same bounded input only if fresh evidence is still needed.',
    });
  else if (diagnosticActive(state) || state === 'not-started')
    Object.assign(result, {
      title: 'Result pending',
      body: 'No authoritative result has been published.',
      finding: 'Pending · no health verdict',
      coverage: 'Collection incomplete',
      tone: 'info',
      next: 'Wait for Job evidence.',
    });

  const rawAvailable =
    outputAvailable &&
    request.detail === 'bounded' &&
    !!definition?.textLineLimit;
  const lines = [
    'source=synthetic-review-fixture',
    `template=${request.id}`,
    `object=${request.scope}`,
    `sample_seconds=${request.sampleSeconds}`,
    ...(storage && state !== 'no-finding'
      ? [
          `member=${storageMembers[0]} carrier=up speed=25000 role=active`,
          `member=${storageMembers[1]} carrier=down speed=unknown role=inactive`,
          state === 'partial'
            ? 'counter_sample=unknown coverage=partial'
            : 'lacp=off bond_mode=active-backup',
        ]
      : [`result=${result.finding}`]),
  ];
  const raw = rawAvailable ? lines.join('\n') : null;
  return {
    ...result,
    outputAvailable,
    raw,
    evidenceAvailable:
      state !== 'evidence-unavailable' &&
      state !== 'not-started' &&
      !diagnosticActive(state),
    limits: {
      bytes: diagnosticOutputBytes,
      textLines: definition?.textLineLimit ?? 0,
    },
    textExcerptLines: raw ? lines.length : 0,
    truncated: state === 'truncated',
  };
}

export function diagnosticExport(
  request: DiagnosticRequest,
  state: DiagnosticJobState,
  reviewOnly = false,
) {
  const result = diagnosticResult(request, state);
  if (!result.outputAvailable) return null;
  return {
    prototype: true,
    reviewOnly,
    job: diagnosticJobId,
    correlationId: diagnosticCorrelation,
    request: { ...request },
    state,
    result: {
      title: result.title,
      finding: result.finding,
      coverage: result.coverage,
    },
    limits: result.limits,
    truncated: result.truncated,
    textExcerptLines: result.textExcerptLines,
    ...(result.raw ? { raw: result.raw } : {}),
  };
}
