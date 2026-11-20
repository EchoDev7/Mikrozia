import { useEffect, useMemo, useRef, useState } from 'react';

import { HttpUtil, TimeFormatter } from '@/utils';
import type { Status } from '@/models/status';

const OVERVIEW_WINDOW = 72;
const SEED_BUCKET_SECONDS = 2;

const SERIES_KEYS = [
  'cpu',
  'mem',
  'diskUsage',
  'netUp',
  'netDown',
  'tcpCount',
  'udpCount',
] as const;

export type OverviewSeriesKey = (typeof SERIES_KEYS)[number];

export interface OverviewHistory {
  series: Record<OverviewSeriesKey, Array<number | null>>;
  labels: string[];
}

interface HistoryPoint {
  t: number;
  v: number | null;
}

interface HistoryWindow {
  series: Record<OverviewSeriesKey, Array<number | null>>;
  times: number[];
}

function emptySeries(): Record<OverviewSeriesKey, Array<number | null>> {
  return Object.fromEntries(SERIES_KEYS.map((key) => [key, [] as Array<number | null>])) as Record<
    OverviewSeriesKey,
    Array<number | null>
  >;
}

function emptyWindow(): HistoryWindow {
  return { series: emptySeries(), times: [] };
}

function sampleOf(status: Status): Record<OverviewSeriesKey, number | null> {
  return {
    cpu: status.metricAvailable('cpu') ? status.cpu.percent : null,
    mem: status.mem.percent,
    diskUsage: status.disk.percent,
    netUp: status.metricAvailable('netIO') ? status.netIO.up : null,
    netDown: status.metricAvailable('netIO') ? status.netIO.down : null,
    tcpCount: status.tcpCount,
    udpCount: status.udpCount,
  };
}

function tailWindow<T>(values: T[]): T[] {
  return values.slice(-OVERVIEW_WINDOW);
}

export function mean(values: Array<number | null>): number {
  const available = values.filter((v): v is number => v != null);
  if (available.length === 0) return 0;
  let total = 0;
  for (const v of available) total += v;
  return total / available.length;
}

export function peak(values: Array<number | null>): number {
  let max: number | null = null;
  for (const v of values) if (v != null && (max == null || v > max)) max = v;
  return max ?? 0;
}

/* the seed bucket must be in the backend's allowedHistoryBuckets whitelist;
   2s is the smallest and matches the status poll cadence */
export function useOverviewHistory(status: Status, hasData: boolean): OverviewHistory {
  const [trend, setTrend] = useState<HistoryWindow>(emptyWindow);
  const resourceScope = status.resourceScope;

  const seededUnscoped = useRef(false);
  const seedState = useRef<{ scope: string; complete: boolean } | null>(null);

  useEffect(() => {
    // The placeholder has no resource scope. Wait for the first real status,
    // then seed each scope once even if polling temporarily loses data.
    if (!hasData || (seedState.current?.scope === resourceScope && seedState.current.complete)) {
      return;
    }
    seedState.current = { scope: resourceScope, complete: false };
    let cancelled = false;
    const seedUnscoped = !seededUnscoped.current;
    const keys = seedUnscoped ? SERIES_KEYS : (['cpu'] as const);

    const seed = async () => {
      const responses = new Map<OverviewSeriesKey, HistoryPoint[]>();
      await Promise.all(
        keys.map(async (key) => {
          const msg = await HttpUtil.get<HistoryPoint[]>(
            `/panel/api/server/history/${key}/${SEED_BUCKET_SECONDS}`,
            undefined,
            { silent: true },
          );
          if (msg?.success && Array.isArray(msg.obj)) responses.set(key, msg.obj);
        }),
      );
      if (cancelled || responses.size === 0) return;
      seedState.current = { scope: resourceScope, complete: true };
      seededUnscoped.current = true;

      const seedTimes = tailWindow(
        [
          ...new Set(
            [...responses.values()].flatMap((points) =>
              points.map((p) => Number(p.t)).filter((ts) => Number.isFinite(ts)),
            ),
          ),
        ].sort((a, b) => a - b),
      );
      if (seedTimes.length === 0) return;
      const seedSeries = emptySeries();
      for (const key of SERIES_KEYS) {
        const byTs = new Map<number, number | null>();
        for (const p of responses.get(key) ?? []) {
          const value = p.v == null ? NaN : Number(p.v);
          byTs.set(Number(p.t) || 0, Number.isFinite(value) ? value : null);
        }
        seedSeries[key] = seedTimes.map((ts) => byTs.get(ts) ?? null);
      }

      setTrend((prev) => {
        const merged = emptyWindow();
        merged.times = tailWindow([...new Set(seedTimes.concat(prev.times))].sort((a, b) => a - b));
        for (const key of SERIES_KEYS) {
          const points = new Map<number, number | null>();
          if (seedUnscoped || key === 'cpu') {
            seedTimes.forEach((ts, index) => points.set(ts, seedSeries[key][index]));
          }
          prev.times.forEach((ts, index) => {
            const previous = prev.series[key][index] ?? null;
            if (previous != null || !points.has(ts)) points.set(ts, previous);
          });
          merged.series[key] = merged.times.map((ts) => points.get(ts) ?? null);
        }
        return merged;
      });
    };

    seed().catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [hasData, resourceScope]);

  // Each polled status is appended during render; an effect would show the
  // chart one sample behind the numbers beside it.
  const [sampledStatus, setSampledStatus] = useState<Status | null>(null);
  if (hasData && status !== sampledStatus) {
    setSampledStatus(status);
    setTrend((prev) => {
      const scopeChanged = sampledStatus && sampledStatus.resourceScope !== status.resourceScope;
      const point = sampleOf(status);
      const next = emptyWindow();
      const sampledAt = Math.max(
        Math.floor(Date.now() / 1000),
        (prev.times[prev.times.length - 1] ?? 0) + 1,
      );
      next.times = tailWindow([...new Set(prev.times.concat(sampledAt))].sort((a, b) => a - b));
      for (const key of SERIES_KEYS) {
        const scoped = key === 'cpu';
        const nextTime = next.times[next.times.length - 1];
        const points = new Map<number, number | null>();
        prev.times.forEach((ts, index) => {
          points.set(ts, scoped && scopeChanged ? null : (prev.series[key][index] ?? null));
        });
        points.set(nextTime, !scoped || status.metricAvailable(key) ? point[key] : null);
        next.series[key] = next.times.map((ts) => points.get(ts) ?? null);
      }
      return next;
    });
  }

  const labels = useMemo(() => trend.times.map(TimeFormatter.formatClock), [trend.times]);

  return useMemo(() => ({ series: trend.series, labels }), [trend.series, labels]);
}
