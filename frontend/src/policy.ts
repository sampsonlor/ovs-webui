import type {
  Candidate,
  NativeVlan,
  Port,
  Session,
  Transaction,
  Validation,
} from '../../clients/typescript/public-v1.generated';

export const standardModes = [
  'access',
  'trunk',
  'native-tagged',
  'native-untagged',
] as const;
export function has(session: Session | null, capability: string): boolean {
  return !!session?.effective_capabilities.includes(capability);
}
export function vlanText(v: NativeVlan | null): string {
  if (!v) return 'Unknown / withheld';
  return `${v.vlan_mode ?? 'Native default'} · tag ${v.tag ?? '—'} · trunks ${v.trunks.length ? v.trunks.join(', ') : 'all VLANs (empty set)'}${v.cvlans.length ? ` · CVLANs ${v.cvlans.join(', ')}` : ''}`;
}
export function editReason(
  p: Port,
  session: Session | null,
  desktop: boolean,
): string {
  if (!desktop) return 'Use a desktop to prepare a configuration change.';
  if (!has(session, 'workspace.write') || !has(session, 'ovs.port.vlan.write'))
    return 'Current permissions do not allow VLAN changes.';
  if (p.vlan.source.freshness !== 'fresh' || p.vlan.availability !== 'known')
    return 'VLAN observation is stale, unavailable or withheld.';
  if (!p.allowed_operations.includes('port.vlan.set'))
    return `VLAN authority: ${typeof p.vlan_ownership === 'string' ? p.vlan_ownership : 'unknown'}. This field group is read-only.`;
  const fields = p.fields as Record<string, { editable?: boolean }> | undefined;
  if (
    !['vlan_mode', 'tag', 'trunks', 'cvlans'].every(
      (f) => fields?.[f]?.editable === true,
    )
  )
    return 'The server has not made every VLAN field editable.';
  const native = p.vlan.native;
  if (
    !native ||
    !standardModes.some((m) => m === native.vlan_mode) ||
    native.cvlans.length ||
    (native.tag !== null && (native.tag < 1 || native.tag > 4094)) ||
    native.trunks.some((v) => v < 1 || v > 4094)
  ) {
    return 'Preserved native, advanced or unknown value. Editing this value is outside the first slice.';
  }
  return '';
}
export function applyReady(
  c: Candidate | null,
  v: Validation | null,
  session: Session | null,
  desktop: boolean,
): boolean {
  return !!(
    desktop &&
    has(session, 'configuration.apply') &&
    has(session, 'ovs.port.vlan.write') &&
    c &&
    v &&
    c.state === 'dirty' &&
    !c.consumed_by &&
    c.safe_apply_available &&
    !c.diff_truncated &&
    v.state === 'passed' &&
    v.usable === true &&
    v.execution_ready === true &&
    c.id === v.candidate_id &&
    c.revision === v.candidate_revision
  );
}
const knownSafety = new Set([
  'preparing',
  'awaiting-confirmation',
  'rollback-requested',
  'rolling-back',
  'confirmed',
  'rolled-back',
  'not-committed',
  'recovery-required',
  'rollback-conflict',
]);
export function decisionReady(
  t: Transaction | null,
  session: Session | null,
  action: 'confirm' | 'rollback',
): boolean {
  return !!(
    t &&
    knownSafety.has(t.safe_apply) &&
    t.allowed_actions.includes(action) &&
    has(
      session,
      action === 'confirm' ? 'configuration.confirm' : 'configuration.rollback',
    ) &&
    (action !== 'confirm' ||
      (t.safe_apply === 'awaiting-confirmation' &&
        t.knowledge === 'known' &&
        t.applied_outcome === 'applied' &&
        t.health === 'healthy'))
  );
}
export function remaining(
  t: Transaction,
  receivedAt: number,
  now: number,
): number | null {
  if (!t.confirmation_deadline) return null;
  const seconds =
    (Date.parse(t.confirmation_deadline) -
      Date.parse(t.server_time) -
      Math.max(0, now - receivedAt)) /
    1000;
  return Number.isFinite(seconds) ? Math.max(0, Math.ceil(seconds)) : null;
}
export function vlanNumbers(text: string): number[] {
  if (!text.trim()) return [];
  const tokens = text.split(',').map((s) => s.trim());
  if (tokens.some((s) => !/^[0-9]{1,4}$/.test(s)))
    throw new Error('Use comma-separated VLAN IDs from 1 to 4094.');
  const values = tokens.map(Number);
  if (
    values.some((n) => n < 1 || n > 4094) ||
    new Set(values).size !== values.length
  )
    throw new Error('VLAN IDs must be unique and between 1 and 4094.');
  return values.sort((a, b) => a - b);
}
