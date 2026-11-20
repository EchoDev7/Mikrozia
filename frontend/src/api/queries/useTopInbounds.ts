import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { TopInboundSchema } from '@/generated/zod';
import type { TopInbound } from '@/generated/types';
import { keys } from '@/api/queryKeys';

export const TOP_INBOUNDS_POLL_MS = 15000;

const TopInboundListSchema = z.array(TopInboundSchema).max(5);

async function fetchTopInbounds(): Promise<TopInbound[]> {
  const msg = await HttpUtil.get('/panel/api/inbounds/top', undefined, { silent: true });
  if (!msg?.success || !Array.isArray(msg.obj)) {
    throw new Error(msg?.msg || 'Failed to fetch top inbounds');
  }
  const validated = parseMsg(msg, TopInboundListSchema, 'inbounds/top', {
    strict: true,
  });
  return validated.obj ?? [];
}

export function useTopInbounds(enabled = true) {
  return useQuery({
    queryKey: keys.inbounds.top(),
    queryFn: fetchTopInbounds,
    enabled,
    refetchInterval: TOP_INBOUNDS_POLL_MS,
    refetchIntervalInBackground: false,
    staleTime: 0,
    retry: false,
  });
}
