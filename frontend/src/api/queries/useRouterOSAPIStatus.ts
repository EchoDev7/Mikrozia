import { useQuery } from '@tanstack/react-query';
import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { RouterOSAPIStatusSchema } from '@/generated/zod';
import type { RouterOSAPIStatus } from '@/generated/types';
import { keys } from '@/api/queryKeys';

export type { RouterOSAPIStatus };

async function fetchStatus(): Promise<RouterOSAPIStatus> {
  const msg = await HttpUtil.get('/panel/api/server/routerosAPIStatus', undefined, {
    silent: true,
  });
  if (!msg?.success || !msg.obj) throw new Error('RouterOS API status unavailable');
  const validated = parseMsg(msg, RouterOSAPIStatusSchema, 'server/routerosAPIStatus', {
    strict: true,
  });
  if (!validated.obj) throw new Error('RouterOS API status unavailable');
  return validated.obj;
}

export function useRouterOSAPIStatus(enabled = true) {
  return useQuery({
    queryKey: keys.server.routerOSStatus(),
    queryFn: fetchStatus,
    enabled,
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
    staleTime: 0,
    retry: false,
  });
}
