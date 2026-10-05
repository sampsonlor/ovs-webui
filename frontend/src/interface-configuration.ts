import type { Interface } from '../../clients/typescript/public-v1.generated';
import { field } from './interface-observation.ts';

export const configurationFields = [
  ['ofport_request', 'Requested OpenFlow port'],
  ['ingress_policing_rate', 'Ingress bandwidth limit'],
  ['ingress_policing_burst', 'Ingress bandwidth burst'],
  ['ingress_policing_kpkts_rate', 'Ingress packet limit'],
  ['ingress_policing_kpkts_burst', 'Ingress packet burst'],
] as const;

export function configurationObservation(
  resource: Interface,
  name: string,
): string {
  const f = field(resource, name);
  if (!f || f.availability === 'unavailable') return 'Unavailable';
  if (f.availability === 'withheld') return 'Withheld';
  if (f.availability === 'unsupported') return 'Unsupported';
  if (f.availability !== 'known' || f.source?.confidence !== 'proven')
    return 'Unknown';
  if (!['fresh', 'stale'].includes(f.source.freshness)) return 'Unknown';
  let result: string;
  if (name === 'ofport_request') {
    if (!Array.isArray(f.value) || f.value.length > 1)
      return 'Unknown native shape';
    if (f.value.length === 0)
      result = 'Automatic allocation (native empty request)';
    else {
      const value = f.value[0];
      if (
        typeof value !== 'string' ||
        !/^[1-9]\d{0,4}$/.test(value) ||
        BigInt(value) > 65279n
      )
        return 'Unknown native shape';
      result = value;
    }
  } else {
    const units: Record<string, string> = {
      ingress_policing_rate: 'kbit/s',
      ingress_policing_burst: 'kbit',
      ingress_policing_kpkts_rate: 'kpps',
      ingress_policing_kpkts_burst: 'kpackets',
    };
    if (
      !units[name] ||
      typeof f.value !== 'string' ||
      !/^(?:0|[1-9]\d{0,18})$/.test(f.value) ||
      BigInt(f.value) > 9223372036854775807n
    )
      return 'Unknown native shape';
    result = `${f.value} ${units[name]}`;
    if (f.value === '0')
      result = name.endsWith('_rate')
        ? `Disabled (${result})`
        : `Native default (${result})`;
  }
  return f.source.freshness === 'stale' ? `Last observed: ${result}` : result;
}
