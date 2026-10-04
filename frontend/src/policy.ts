import type {
  Candidate,
  Interface,
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
export const vlanModes = [...standardModes, 'dot1q-tunnel'] as const;
export function has(session: Session | null, capability: string): boolean {
  return !!session?.effective_capabilities.includes(capability);
}
export function mtuNumber(value: unknown): number | null {
  if (
    !Array.isArray(value) ||
    value.length !== 1 ||
    typeof value[0] !== 'string' ||
    !/^[0-9]+$/.test(value[0])
  )
    return null;
  const n = Number(value[0]);
  return Number.isSafeInteger(n) && n >= 576 && n <= 65535 ? n : null;
}
export function mtuEditReason(
  item: Interface,
  session: Session | null,
  desktop: boolean,
): string {
  if (!desktop) return 'Use a desktop to prepare a configuration change.';
  if (
    !has(session, 'workspace.write') ||
    !has(session, 'ovs.interface.mtu.write')
  )
    return 'Current permissions do not allow Interface MTU changes.';
  if (
    item.source.freshness !== 'fresh' ||
    item.fields?.mtu_request?.availability !== 'known'
  )
    return 'MTU configuration is stale, unavailable or withheld.';
  if (
    Array.isArray(item.fields?.mtu_request?.value) &&
    item.fields.mtu_request.value.length === 0 &&
    Number.isSafeInteger(item.mtu_default) &&
    (item.fields?.mtu?.availability !== 'known' ||
      mtuNumber(item.fields?.mtu?.value) !== item.mtu_default)
  )
    return 'Automatic MTU is not proven by the current device observation. Review the actual value and Bridge devices.';
  if (
    item.mtu_editable !== true ||
    !item.allowed_operations?.includes('interface.mtu.set') ||
    item.fields?.mtu_request?.editable !== true ||
    (mtuNumber(item.fields?.mtu_request?.value) === null &&
      !(
        Array.isArray(item.fields?.mtu_request?.value) &&
        item.fields.mtu_request.value.length === 0 &&
        typeof item.mtu_default === 'number' &&
        Number.isSafeInteger(item.mtu_default) &&
        item.mtu_default >= 576 &&
        item.mtu_default <= 65535
      ))
  )
    return 'MTU editing requires an explicitly authorized standalone internal Interface with a valid request or proven automatic MTU. Local interfaces, Bond members, other types and unproven defaults are read-only.';
  return '';
}
export function vlanText(v: NativeVlan | null): string {
  if (!v) return 'Unknown / withheld';
  if (v.vlan_mode === 'dot1q-tunnel')
    return `QinQ · service VLAN ${v.tag ?? '—'} · customer VLANs ${v.cvlans.length ? v.cvlans.join(', ') : 'all customer VLANs (empty set)'}`;
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
    !vlanModes.some((m) => m === native.vlan_mode) ||
    (native.vlan_mode === 'dot1q-tunnel'
      ? p.qinq_editable !== true ||
        native.tag === null ||
        native.trunks.length > 0
      : native.cvlans.length > 0) ||
    native.cvlans.some((v) => v < 1 || v > 4094) ||
    (native.tag !== null && (native.tag < 1 || native.tag > 4094)) ||
    native.trunks.some((v) => v < 1 || v > 4094)
  ) {
    return 'This native configuration or QinQ context has not passed the supported editing gates.';
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
    c &&
    c.intents.length > 0 &&
    c.intents.every((i) => {
      const capability = (
        {
          'port.vlan.set': 'ovs.port.vlan.write',
          'bond.configure': 'ovs.port.bond.write',
          'port.lacp.set': 'ovs.port.bond.write',
          'bridge.create-isolated': 'ovs.bridge.create',
          'bridge.delete-isolated': 'ovs.bridge.delete',
          'port.create-internal': 'ovs.port.internal.create',
          'port.delete-internal': 'ovs.port.internal.delete',
          'interface.mtu.set': 'ovs.interface.mtu.write',
          'interface.mtu.clear': 'ovs.interface.mtu.write',
        } as Record<string, string>
      )[i.operation];
      return !!capability && has(session, capability);
    }) &&
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
