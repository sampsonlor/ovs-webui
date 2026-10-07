import type {
  Interface,
  LinuxPolicingAction,
} from '../../clients/typescript/public-v1.generated';

export function policingReady(item: Interface): boolean {
  const value = item.linux_ingress_policing;
  return (
    !!value &&
    ['known', 'partial'].includes(value.availability) &&
    value.source.freshness === 'fresh' &&
    value.source.confidence === 'proven' &&
    value.source.authority === 'linux-netlink-observation'
  );
}

export function policingSummary(item: Interface): string {
  const value = item.linux_ingress_policing;
  if (value?.availability === 'withheld')
    return 'Configuration read permission is required to view installed rules.';
  if (policingReady(item)) {
    if (value!.availability === 'partial')
      return 'Partial coverage. Some ingress rules or actions could not be interpreted; an empty list does not establish absence of policing.';
    return value!.actions.length
      ? 'Police actions observed in Linux ingress rules.'
      : 'No police action observed in the supported Linux ingress rules. This does not establish that traffic is unrestricted.';
  }
  switch (value?.reason) {
    case 'OVS_ASSOCIATION_STALE':
      return 'The OVS association is stale. Refresh before relying on kernel observations.';
    case 'OVS_DEVICE_BINDING_UNPROVEN':
      return 'No proven Linux device association for this Interface type or native ifindex.';
    case 'OVS_DEVICE_BINDING_CHANGED':
    case 'LINUX_IFINDEX_MISMATCH':
      return 'The Interface and host device identities no longer match. Previous rule values are not shown.';
    case 'LINUX_POLICING_ACCESS_DENIED':
      return 'The service could not read Linux ingress rules with its current host permissions.';
    case 'LINUX_OBSERVATION_TIMEOUT':
    case 'LINUX_PROVIDER_BUSY':
      return 'The bounded Linux observation is temporarily unavailable. Refresh to try again.';
    case 'LINUX_POLICING_DUMP_UNPROVEN':
      return 'The Linux rule read was incomplete or unsupported. No absence or enforcement claim can be made.';
    default:
      return 'Linux ingress policing observations are unavailable.';
  }
}

export function policingRate(
  action: LinuxPolicingAction,
  field: 'bytes_per_second' | 'packets_per_second',
): string {
  const value = action[field];
  if (value === null) return 'Not reported';
  if (
    typeof value !== 'string' ||
    !/^(0|[1-9][0-9]{0,19})$/.test(value) ||
    BigInt(value) > BigInt('18446744073709551615')
  )
    return 'Unknown';
  return `${value} ${field === 'bytes_per_second' ? 'bytes/s' : 'packets/s'}`;
}
