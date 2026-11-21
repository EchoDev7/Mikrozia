import { useQuery } from '@tanstack/react-query';
import { ContainerLogsSnapshotSchema } from '@/generated/zod';
import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';

async function fetchLogs(count: number) {
  const msg = await HttpUtil.get(`/panel/api/server/routerosContainerLogs/${count}`, undefined, {
    silent: true,
  });
  if (!msg?.success || !msg.obj) throw new Error('RouterOS container logs unavailable');
  const validated = parseMsg(msg, ContainerLogsSnapshotSchema, 'server/routerosContainerLogs', {
    strict: true,
  });
  if (!validated.obj) throw new Error('RouterOS container logs unavailable');
  return validated.obj;
}

export function useRouterOSContainerLogs(count: number, enabled: boolean, autoUpdate: boolean) {
  return useQuery({
    queryKey: keys.server.routerOSContainerLogs(count),
    queryFn: () => fetchLogs(count),
    enabled,
    refetchInterval: autoUpdate ? 5000 : false,
    refetchIntervalInBackground: false,
    staleTime: 0,
    retry: false,
  });
}
