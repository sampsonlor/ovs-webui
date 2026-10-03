import type {
  Interface,
  InventoryField,
} from '../../clients/typescript/public-v1.generated';

export const interfaceFields = [
  ['type', 'Native type'],
  ['ofport', 'OpenFlow port'],
  ['admin_state', 'OVS admin state'],
  ['link_state', 'OVS link state'],
  ['mtu', 'Observed MTU'],
  ['mtu_request', 'Requested MTU'],
  ['ifindex', 'Interface index'],
  ['link_speed', 'Link speed (bit/s)'],
  ['duplex', 'Duplex'],
  ['error', 'Provider error'],
] as const;

export function field(
  resource: Interface,
  name: string,
): InventoryField | undefined {
  return resource.fields?.[name];
}
export function availability(resource: Interface, name: string): string {
  return field(resource, name)?.availability ?? 'unavailable';
}
export function observation(resource: Interface, name: string): string {
  const value = field(resource, name);
  if (!value) return 'Unavailable';
  if (value.availability === 'withheld') return 'Withheld';
  if (value.availability !== 'known') return 'Unknown';
  const native = Array.isArray(value.value) ? value.value : [value.value];
  if (native.length === 0 || native[0] === null || native[0] === undefined) {
    return (
      (
        {
          mtu_request: 'Not requested (native default)',
          ofport: 'Not assigned',
          error: 'No provider error reported',
        } as Record<string, string>
      )[name] ?? 'Not reported'
    );
  }
  if (native.length !== 1) return 'Unknown native shape';
  if (name === 'type' && native[0] === '') return 'system (native default)';
  if (name === 'ofport' && String(native[0]) === '-1')
    return '-1 · allocation failed';
  if (name === 'error') return 'Provider reported an error';
  if (!['string', 'number', 'boolean'].includes(typeof native[0]))
    return 'Unknown native shape';
  return String(native[0]);
}
export function deviceObservation(resource: Interface, key: string): string {
  const value = field(resource, 'status');
  if (!value || value.availability !== 'known')
    return observation(resource, 'status');
  if (
    !value.value ||
    typeof value.value !== 'object' ||
    Array.isArray(value.value)
  )
    return 'Unknown';
  const native = (value.value as Record<string, unknown>)[key];
  return typeof native === 'string' ? native : 'Not reported';
}
export function pciAssociation(resource: Interface): string {
  const bus = deviceObservation(resource, 'bus_info');
  // A native bus string is the sole evidence. Names/type do not prove a PCI device.
  return /^(?:pci:)?[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$/i.test(bus)
    ? bus
    : ['Unavailable', 'Unknown', 'Withheld'].includes(bus)
      ? bus
      : 'Not proven';
}
