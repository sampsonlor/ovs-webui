import type { ChangeIntent } from '../app/prototype-model';

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
): string | null {
  if (width < 768) return 'Start bounded diagnostics on tablet or desktop.';
  if (busy) return 'The current diagnostic Job is still active.';
  if (input !== 'valid') return 'Resolve diagnostic input validation first.';
  if (!runnableDiagnostics.some((item) => item === id))
    return 'Diagnostic permission or provider is unavailable.';
  if (!/^(Port|Bridge)\/[a-zA-Z0-9_.-]{1,63}$/.test(scope))
    return 'Choose one explicit Port or Bridge scope.';
  return null;
}

export function representativeBondIntent(
  data: Record<string, unknown>,
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
    bridge === 'br-storage'
      ? ['enp129s0f0', 'enp129s0f1']
      : bridge === 'br-mgmt'
        ? ['eno1', 'eno2']
        : ['enp65s0f2', 'enp65s0f3'];
  return {
    kind: 'bond',
    objectType: 'Port',
    objectName: name,
    bridgeName: bridge,
    title: `Create Bond Port / ${name}`,
    summary: `Attach 2 Interface members on ${bridge} · ${mode} · LACP ${lacp}`,
    current: 'object: absent',
    candidate: `bridge: ${bridge}\nbond_mode: ${mode}\nlacp: ${lacp}\ninterfaces: [${members.join(', ')}]`,
    risk: bridge === 'br-mgmt' ? 'High' : 'Medium',
    capability: 'bond.manage',
    evidenceObject: `Port/${name}`,
  };
}
