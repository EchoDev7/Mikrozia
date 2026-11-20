import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { CLIENTS_SUMMARY_POLL_MS, useClientsSummary } from '@/api/queries/useClientsSummary';
import { Status } from '@/models/status';
import ClientsSummaryTile from '@/pages/index/ClientsSummaryTile';
import IndexPage from '@/pages/index/IndexPage';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('uplot', () => ({
  default: class {
    static paths = { spline: () => undefined };
    static pxRatio = 1;
    setData() {}
    setSize() {}
    redraw() {}
    destroy() {}
  },
}));
vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));
vi.mock('@/components/sponsor/SponsorSlot', () => ({ default: () => null }));
vi.mock('@/api/queries/useStatusQuery', () => {
  let cachedStatus: Status | null = null;
  const refresh = vi.fn();
  return {
    useStatusQuery: () => ({
      status: (cachedStatus ??= new Status({
        cpu: 12,
        mem: { current: 100, total: 1000 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      })),
      fetched: true,
      fetchError: '',
      refresh,
    }),
  };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('ClientsSummaryTile', () => {
  it('renders Clients heading, total, and five statuses in exact order with no PII', () => {
    const { container } = renderWithProviders(
      <ClientsSummaryTile
        summary={{ total: 25, online: 4, ended: 2, depleting: 3, disabled: 5, active: 15 }}
        isLoading={false}
        isError={false}
      />,
    );

    expect(screen.getByText('Clients')).toBeInTheDocument();
    expect(screen.getByText('25')).toBeInTheDocument();

    const rows = Array.from(container.querySelectorAll('.ov-clients-row'));
    expect(rows.map((r) => r.querySelector('.ov-clients-label')?.textContent?.trim())).toEqual([
      'Online',
      'Ended',
      'Depleting',
      'Disabled',
      'Active',
    ]);
    expect(rows.map((r) => r.querySelector('.ov-clients-val')?.textContent?.trim())).toEqual([
      '4',
      '2',
      '3',
      '5',
      '15',
    ]);
    expect(container.textContent).not.toMatch(/@|uuid|http/i);
  });

  it('renders six real zeroes for valid zero-client response', () => {
    const { container } = renderWithProviders(
      <ClientsSummaryTile
        summary={{ total: 0, online: 0, ended: 0, depleting: 0, disabled: 0, active: 0 }}
        isLoading={false}
        isError={false}
      />,
    );

    expect(screen.queryByText('Unavailable')).toBeNull();
    expect(screen.getByText('0', { selector: '.ov-tile-number' })).toBeInTheDocument();
    const vals = Array.from(container.querySelectorAll('.ov-clients-val')).map((el) =>
      el.textContent?.trim(),
    );
    expect(vals).toEqual(['0', '0', '0', '0', '0']);
  });

  it('renders Unavailable on API error without breaking', () => {
    const { container } = renderWithProviders(
      <ClientsSummaryTile summary={undefined} isLoading={false} isError />,
    );

    expect(screen.getByText('Unavailable')).toBeInTheDocument();
    const vals = Array.from(container.querySelectorAll('.ov-clients-val')).map((el) =>
      el.textContent?.trim(),
    );
    expect(vals).toEqual(['—', '—', '—', '—', '—']);
  });
});

describe('useClientsSummary query contract', () => {
  it('polls /panel/api/clients/summary every 15s and disables background polling', async () => {
    expect(CLIENTS_SUMMARY_POLL_MS).toBe(15000);
    const getSpy = vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: true,
      msg: '',
      obj: { total: 1, online: 0, ended: 0, depleting: 0, disabled: 0, active: 1 },
    });

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useClientsSummary(true), { wrapper });
    await waitFor(() =>
      expect(result.current.data).toEqual({
        total: 1,
        online: 0,
        ended: 0,
        depleting: 0,
        disabled: 0,
        active: 1,
      }),
    );
    expect(getSpy).toHaveBeenCalledWith('/panel/api/clients/summary', undefined, { silent: true });

    const query = qc.getQueryCache().find({ queryKey: ['clients', 'summary'] });
    const opts = query?.options as {
      refetchInterval?: number;
      refetchIntervalInBackground?: boolean;
    };
    expect(opts.refetchInterval).toBe(15000);
    expect(opts.refetchIntervalInBackground).toBe(false);
  });
});

describe('IndexPage Dashboard integration', () => {
  it('replaces Swap with Clients while keeping CPU, RAM, Storage, and Xray Metrics intact', async () => {
    vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
      if (url === '/panel/api/clients/summary') {
        return {
          success: true,
          msg: '',
          obj: { total: 6, online: 1, ended: 1, depleting: 1, disabled: 1, active: 2 },
        };
      }
      if (url === '/panel/api/server/routerosAPIStatus') {
        return {
          success: true,
          msg: '',
          obj: {
            state: 'not_configured',
            checkedAt: 0,
            stale: false,
            cpuHistory: [],
            memHistory: [],
          },
        };
      }
      return { success: true, msg: '', obj: [] };
    });
    vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, msg: '', obj: {} });

    const { container } = renderWithProviders(
      <MemoryRouter>
        <IndexPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('Clients')).toBeInTheDocument();
    expect(screen.getByText('6')).toBeInTheDocument();
    expect(screen.queryByText('Swap')).toBeNull();
    const vitalsText = container.querySelector('.ov-vitals')?.textContent ?? '';
    expect(vitalsText).toContain('CPU');
    expect(vitalsText).toContain('RAM');
    expect(vitalsText).toContain('Storage');
    expect(vitalsText).toContain('Clients');
    expect(vitalsText).not.toContain('Swap');
    expect(screen.getByRole('button', { name: 'Xray Metrics' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Xray Metrics' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });
});
