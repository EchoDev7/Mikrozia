import { useQuery } from '@tanstack/react-query';
import { ContainerSnapshotSchema } from '@/generated/zod';
import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';

async function fetchStatus() {
  const msg = await HttpUtil.get('/panel/api/server/routerosContainerStatus', undefined, {
    silent: true,
  });
  if (!msg?.success || !msg.obj) throw new Error('RouterOS container status unavailable');
  const validated = parseMsg(msg, ContainerSnapshotSchema, 'server/routerosContainerStatus', {
    strict: true,
  });
  if (!validated.obj) throw new Error('RouterOS container status unavailable');
  return validated.obj;
}

export function useRouterOSContainerStatus(enabled = true) {
  return useQuery({
    queryKey: keys.server.routerOSContainerStatus(),
    queryFn: fetchStatus,
    enabled,
    refetchInterval: 15000,
    refetchIntervalInBackground: false,
    staleTime: 0,
    retry: false,
  });
}
