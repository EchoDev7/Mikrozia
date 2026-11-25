import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { TOP_INBOUNDS_POLL_MS, useTopInbounds } from '@/api/queries/useTopInbounds';
import { Status } from '@/models/status';
import TopInboundsTile from '@/pages/index/TopInboundsTile';
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

describe('TopInboundsTile', () => {
  it('renders up to 5 rows in exact API order, remark fallback, Disabled badge, and valid 0 B', () => {
    const { container } = renderWithProviders(
      <TopInboundsTile
        isLoading={false}
        isError={false}
        inbounds={[
          {
            id: 5,
            remark: 'VLESS-443',
            protocol: 'vless',
            port: 443,
            traffic: 2048,
            enabled: true,
          },
          { id: 2, remark: '   ', protocol: 'trojan', port: 8443, traffic: 1024, enabled: false },
          { id: 9, remark: 'Zero-Node', protocol: 'vmess', port: 10000, traffic: 0, enabled: true },
        ]}
      />,
    );

    expect(screen.getByText('Top Inbounds')).toBeInTheDocument();
    const rows = Array.from(container.querySelectorAll('.ov-top-inbounds-row'));
    expect(rows).toHaveLength(3);
    expect(rows[0].textContent).toContain('1');
    expect(rows[0].textContent).toContain('VLESS-443');
    expect(rows[0].textContent).toContain('2.00 KB');
    expect(rows[0].textContent).not.toContain('Disabled');

    expect(rows[1].textContent).toContain('2');
    expect(rows[1].textContent).toContain('trojan:8443');
    expect(rows[1].textContent).toContain('Disabled');
    expect(rows[1].textContent).toContain('1.00 KB');

    expect(rows[2].textContent).toContain('3');
    expect(rows[2].textContent).toContain('Zero-Node');
    expect(rows[2].textContent).toContain('0 B');
    expect(screen.queryByText('Unavailable')).toBeNull();
    expect(container.querySelectorAll('a, button')).toHaveLength(0);
  });

  it('caps at 5 rows and renders empty and error states safely', () => {
    const { rerender, container } = renderWithProviders(
      <TopInboundsTile
        isLoading={false}
        isError={false}
        inbounds={[
          { id: 1, remark: 'A', protocol: 'vless', port: 443, traffic: 600, enabled: true },
          { id: 2, remark: 'B', protocol: 'vless', port: 444, traffic: 500, enabled: true },
          { id: 3, remark: 'C', protocol: 'vless', port: 445, traffic: 400, enabled: true },
          { id: 4, remark: 'D', protocol: 'vless', port: 446, traffic: 300, enabled: true },
          { id: 5, remark: 'E', protocol: 'vless', port: 447, traffic: 200, enabled: true },
          { id: 6, remark: 'F-extra', protocol: 'vless', port: 448, traffic: 100, enabled: true },
        ]}
      />,
    );

    expect(container.querySelectorAll('.ov-top-inbounds-row')).toHaveLength(5);
    expect(screen.queryByText('F-extra')).toBeNull();

    rerender(<TopInboundsTile isLoading={false} isError={false} inbounds={[]} />);
    expect(screen.getByText('No inbounds')).toBeInTheDocument();

    rerender(<TopInboundsTile isLoading={false} isError={true} inbounds={undefined} />);
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
  });
});

describe('useTopInbounds query contract', () => {
  it('polls /panel/api/inbounds/top every 15s and disables background polling', async () => {
    const getSpy = vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: true,
      msg: '',
      obj: [
        { id: 1, remark: 'VLESS-443', protocol: 'vless', port: 443, traffic: 1024, enabled: true },
      ],
    });

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );

    expect(TOP_INBOUNDS_POLL_MS).toBe(15000);
    const { result } = renderHook(() => useTopInbounds(true), { wrapper });

    await waitFor(() =>
      expect(result.current.data).toEqual([
        { id: 1, remark: 'VLESS-443', protocol: 'vless', port: 443, traffic: 1024, enabled: true },
      ]),
    );
    expect(getSpy).toHaveBeenCalledWith('/panel/api/inbounds/top', undefined, { silent: true });

    const query = qc.getQueryCache().find({ queryKey: ['inbounds', 'top'] });
    const opts = query?.options as {
      refetchInterval?: number;
      refetchIntervalInBackground?: boolean;
    };
    expect(opts.refetchInterval).toBe(15000);
    expect(opts.refetchIntervalInBackground).toBe(false);
  });
});

describe('IndexPage Top Inbounds placement', () => {
  it('places Top Inbounds between Clients and Storage while keeping other Dashboard cards intact', async () => {
    vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
      if (url === '/panel/api/clients/summary') {
        return {
          success: true,
          msg: '',
          obj: { total: 4, online: 1, ended: 0, depleting: 1, disabled: 0, active: 2 },
        };
      }
      if (url === '/panel/api/inbounds/top') {
        return {
          success: true,
          msg: '',
          obj: [
            {
              id: 7,
              remark: 'Top-Edge',
              protocol: 'vless',
              port: 443,
              traffic: 4096,
              enabled: true,
            },
          ],
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

    expect(await screen.findByText('Top Inbounds')).toBeInTheDocument();
    expect(await screen.findByText('Top-Edge')).toBeInTheDocument();
    expect(screen.getByText('4.00 KB')).toBeInTheDocument();

    const cards = Array.from(container.querySelectorAll('.ov-vitals > *'));
    expect(cards).toHaveLength(6);
    const kickers = cards.map(
      (card) => card.querySelector('.ov-kicker')?.textContent?.trim() ?? '',
    );
    expect(kickers).toEqual([
      'CPU',
      'RAM',
      'Clients',
      'Top Inbounds',
      'Storage',
      'SSL Certificate',
    ]);

    expect(screen.getByText('Overall Speed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Xray Metrics' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Xray Metrics' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });
});
