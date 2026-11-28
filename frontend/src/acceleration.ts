import type { Session } from '../../clients/typescript/public-v1.generated';
import { has } from './policy.ts';

export const dpdkRoute = '/visibility/dpdk';
export const offloadRoute = '/visibility/offload';
export type AccelerationKind = 'dpdk' | 'offload';

export function accelerationKind(path: string): AccelerationKind | null {
  return path === dpdkRoute ? 'dpdk' : path === offloadRoute ? 'offload' : null;
}

export function canReadAcceleration(session: Session | null): boolean {
  return has(session, 'state.read') && has(session, 'inventory.read');
}
