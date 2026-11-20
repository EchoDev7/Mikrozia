import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { ClientsSummaryCountsSchema } from '@/generated/zod';
import type { ClientsSummaryCounts } from '@/generated/types';
import { keys } from '@/api/queryKeys';

export const CLIENTS_SUMMARY_POLL_MS = 15000;

async function fetchClientsSummary(): Promise<ClientsSummaryCounts> {
  const msg = await HttpUtil.get('/panel/api/clients/summary', undefined, { silent: true });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch clients summary');
  const validated = parseMsg(msg, ClientsSummaryCountsSchema, 'clients/summary', {
    strict: true,
  });
  if (!validated.obj) throw new Error('Empty clients summary');
  return validated.obj;
}

export function useClientsSummary(enabled = true) {
  return useQuery({
    queryKey: keys.clients.summary(),
    queryFn: fetchClientsSummary,
    enabled,
    refetchInterval: CLIENTS_SUMMARY_POLL_MS,
    refetchIntervalInBackground: false,
    staleTime: 0,
    retry: false,
  });
}
