import { useQuery } from '@tanstack/react-query';
import { HttpUtil } from '@/utils';

export interface ManagedNatRule {
  id: string;
  inboundId: number;
  service?: string;
  protocol: string;
  dstPort: string;
  toAddress: string;
  toPort: string;
  inInterface?: string;
  dstAddressType?: string;
  disabled: boolean;
  bytes: number;
  packets: number;
  comment: string;
}

async function fetchRouterosNatRules(): Promise<ManagedNatRule[]> {
  const res = (await HttpUtil.get('/panel/api/setting/routerosNatRules', undefined, {
    silent: true,
  })) as { success?: boolean; obj?: ManagedNatRule[] };
  if (!res?.success || !Array.isArray(res.obj)) {
    return [];
  }
  return res.obj;
}

export function useRouterosNatRules(enabled: boolean) {
  return useQuery({
    queryKey: ['routeros', 'natRules'] as const,
    queryFn: fetchRouterosNatRules,
    enabled,
    refetchInterval: 15000,
    refetchIntervalInBackground: false,
    retry: false,
  });
}
