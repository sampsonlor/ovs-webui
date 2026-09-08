import type {
  ChangeIntent,
  DiagnosticParameters,
  DiagnosticRequest,
} from '../app/prototype-model';
import {
  diagnosticParameterErrors,
  diagnosticScopeError,
  diagnosticServiceBlock,
} from './diagnostics-model.ts';
import {
  bondChangeIntent,
  memberOptions,
  type BondDraft,
} from './switching-model.ts';

export const runnableDiagnostics = [
  'diag.net.link-lacp',
  'diag.ovs.datapath-trace',
  'diag.openflow.collection',
] as const;

export function diagnosticRunBlock(
  id: string,
  scope: string,
  input: string,
  busy: boolean,
  width: number,
  parameters?: DiagnosticParameters,
  scenario = 'normal',
): string | null {
  if (width < 768) return 'Start bounded diagnostics on tablet or desktop.';
  if (busy) return 'The current diagnostic Job is still active.';
  const serviceBlocked = diagnosticServiceBlock(scenario);
  if (serviceBlocked) return serviceBlocked;
  if (scenario === 'empty')
    return 'No diagnostic templates are available in the current catalog.';
  if (input !== 'valid') return 'Resolve diagnostic input validation first.';
  if (!runnableDiagnostics.some((item) => item === id))
    return 'Diagnostic permission or provider is unavailable.';
  const scopeError = diagnosticScopeError(id, scope);
  if (scopeError) return scopeError;
  if (parameters) {
    const errors = diagnosticParameterErrors(id, parameters);
    if (errors.sample || errors.detail) return errors.sample || errors.detail;
  }
  return null;
}

export function captureDiagnosticRequest(
  id: string,
  scope: string,
  parameters: DiagnosticParameters,
): DiagnosticRequest {
  if (!runnableDiagnostics.some((item) => item === id))
    throw new Error('Choose an available diagnostic template.');
  const scopeError = diagnosticScopeError(id, scope);
  if (scopeError) throw new Error(scopeError);
  const errors = diagnosticParameterErrors(id, parameters);
  if (errors.sample || errors.detail)
    throw new Error(errors.sample || errors.detail!);
  return {
    id,
    scope,
    sampleSeconds: parameters.sampleSeconds,
    detail: parameters.detail,
  };
}

export function representativeBondIntent(
  data: Record<string, unknown>,
  memberDown = false,
): ChangeIntent {
  const { name, bridge, mode, lacp } = data;
  if (typeof name !== 'string' || !/^[a-zA-Z0-9_.-]{1,63}$/.test(name))
    throw new Error('Choose a valid Bond Port name.');
  if (
    typeof bridge !== 'string' ||
    !['br-fabric', 'br-storage', 'br-mgmt'].includes(bridge)
  )
    throw new Error('Choose a managed Bridge.');
  if (
    typeof mode !== 'string' ||
    !['balance-tcp', 'active-backup', 'balance-slb'].includes(mode)
  )
    throw new Error('Unsupported Bond mode.');
  if (
    typeof lacp !== 'string' ||
    !['active', 'passive', 'off'].includes(lacp) ||
    (mode === 'balance-tcp' && lacp === 'off')
  )
    throw new Error('Choose a compatible LACP policy.');
  const members =
    data.members === undefined
      ? memberOptions(bridge)
          .filter((option) => option.available)
          .slice(0, 2)
          .map((option) => option.name)
      : data.members;
  if (
    !Array.isArray(members) ||
    !members.every((member) => typeof member === 'string')
  )
    throw new Error('Members must be an array of Interface names.');
  const minLinks = data.minLinks === undefined ? 1 : data.minLinks;
  if (typeof minLinks !== 'number' || !Number.isInteger(minLinks))
    throw new Error('Minimum links must be an integer.');
  return bondChangeIntent(
    {
      name,
      bridge,
      mode: mode as BondDraft['mode'],
      lacp: lacp as BondDraft['lacp'],
      minLinks: String(minLinks),
      members,
    },
    undefined,
    false,
    memberDown,
  );
}
