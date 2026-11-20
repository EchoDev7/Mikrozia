import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { Status } from '@/models/status';
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
const { setMockStatus, getMockStatus } = vi.hoisted(() => {
  let current: Status | null = null;
  return {
    setMockStatus: (s: Status) => {
      current = s;
    },
    getMockStatus: () => current,
  };
});

vi.mock('@/api/queries/useStatusQuery', () => {
  const refresh = vi.fn();
  let fallbackStatus: Status | null = null;
  return {
    useStatusQuery: () => ({
      status:
        getMockStatus() ??
        (fallbackStatus ??= new Status({
          cpu: 99.9,
          cpuCores: 16,
          logicalPro: 32,
          cpuSpeedMhz: 3600,
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
  setMockStatus(
    new Status({
      cpu: 99.9,
      cpuCores: 16,
      logicalPro: 32,
      cpuSpeedMhz: 3600,
      mem: { current: 100, total: 1000 },
      disk: { current: 50, total: 1000 },
      xray: { state: 'running', version: '26.10.21' },
    }),
  );
});

function mockDashboardApis(routerosObj: unknown, rejectRouterOS = false) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url === '/panel/api/server/routerosAPIStatus') {
      if (rejectRouterOS) {
        throw new Error('network error');
      }
      return { success: true, msg: '', obj: routerosObj };
    }
    if (url === '/panel/api/clients/summary') {
      return {
        success: true,
        msg: '',
        obj: { total: 4, online: 1, ended: 0, depleting: 1, disabled: 0, active: 2 },
      };
    }
    if (url === '/panel/api/inbounds/top') {
      return { success: true, msg: '', obj: [] };
    }
    return { success: true, msg: '', obj: [] };
  });
  vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, msg: '', obj: {} });
}

function renderIndex() {
  return renderWithProviders(
    <MemoryRouter>
      <IndexPage />
    </MemoryRouter>,
  );
}

describe('Dashboard CPU card RouterOS wiring', () => {
  it('renders valid RouterOS 0% CPU and logical CPUs + GHz without using status.cpu', async () => {
    mockDashboardApis({
      state: 'connected',
      checkedAt: 1700000000,
      stale: false,
      cpuLoad: 0,
      cpuCount: 4,
      cpuFrequencyMhz: 2400,
      cpuAvg: 0,
      cpuPeak: 0,
      cpuHistory: [0],
      memHistory: [],
    });

    const { container } = renderIndex();
    expect(await screen.findByText('4 logical CPUs · 2.40 GHz')).toBeInTheDocument();

    const cpuCard = container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.textContent).toContain('0.0');
    expect(cpuCard.textContent).not.toContain('99.9');
    expect(cpuCard.textContent).not.toContain('32');
    expect(cpuCard.textContent?.toLowerCase()).not.toContain('physical core');
    expect(cpuCard.textContent).toContain('0%');

    const vitalsText = container.querySelector('.ov-vitals')?.textContent ?? '';
    expect(vitalsText).toContain('RAM');
    expect(vitalsText).toContain('Storage');
    expect(vitalsText).toContain('Clients');
    expect(vitalsText).toContain('Top Inbounds');
    expect(screen.getByText('Overall Speed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Xray Metrics' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  it('renders nonzero RouterOS CPU, no-frequency fallback, no-history dash, and stale badge', async () => {
    mockDashboardApis({
      state: 'connected',
      checkedAt: 1700000000,
      stale: true,
      cpuLoad: 37,
      cpuCount: 2,
      cpuFrequencyMhz: null,
      cpuAvg: null,
      cpuPeak: null,
      cpuHistory: [],
      memHistory: [],
    });

    const { container } = renderIndex();
    expect(await screen.findByText('2 logical CPUs')).toBeInTheDocument();

    const cpuCard = container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.textContent).toContain('37.0');
    expect(cpuCard.textContent).toContain('Stale');
    expect(cpuCard.textContent).not.toContain('GHz');
    expect(cpuCard.textContent).not.toContain('99.9');
    expect(cpuCard.querySelector('.ov-tile-foot')?.textContent).toBe('——');
  });

  it('renders RouterOS API not configured when state is not_configured', async () => {
    mockDashboardApis({
      state: 'not_configured',
      checkedAt: 0,
      stale: false,
      cpuLoad: null,
      cpuCount: null,
      cpuFrequencyMhz: null,
      cpuAvg: null,
      cpuPeak: null,
      cpuHistory: [],
      memHistory: [],
    });

    const { container } = renderIndex();
    const cpuCard = container.querySelectorAll('.ov-vitals > *')[0];
    await waitFor(() => expect(cpuCard.textContent).toContain('RouterOS API not configured'));

    expect(cpuCard.textContent).not.toContain('0.0');
    expect(cpuCard.textContent).not.toContain('99.9');
    expect(cpuCard.textContent).not.toContain('logical CPUs');
  });

  it('renders neutral dash when connecting before first sample', async () => {
    mockDashboardApis({
      state: 'connecting',
      checkedAt: 0,
      stale: false,
      cpuLoad: null,
      cpuCount: null,
      cpuFrequencyMhz: null,
      cpuAvg: null,
      cpuPeak: null,
      cpuHistory: [],
      memHistory: [],
    });

    const { container } = renderIndex();
    await screen.findByText('Clients');

    const cpuCard = container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.querySelector('.ov-tile-number')?.textContent).toBe('—');
    expect(cpuCard.textContent).not.toContain('0.0');
    expect(cpuCard.textContent).not.toContain('Unavailable');
  });

  it('renders Unavailable and never 0% on unreachable or API error', async () => {
    mockDashboardApis(
      {
        state: 'unreachable',
        checkedAt: 1700000000,
        stale: false,
        cpuLoad: null,
        cpuCount: null,
        cpuFrequencyMhz: null,
        cpuAvg: null,
        cpuPeak: null,
        cpuHistory: [],
        memHistory: [],
      },
      false,
    );

    const { container } = renderIndex();
    const cpuCard = container.querySelectorAll('.ov-vitals > *')[0];
    await waitFor(() => expect(cpuCard.textContent).toContain('Unavailable'));
    expect(cpuCard.textContent).not.toContain('0.0');
    expect(cpuCard.textContent).not.toContain('99.9');

    cleanup();
    mockDashboardApis(null, true);
    const second = renderIndex();
    const secondCpu = second.container.querySelectorAll('.ov-vitals > *')[0];
    await waitFor(() => expect(secondCpu.textContent).toContain('Unavailable'));
    expect(secondCpu.textContent).not.toContain('0.0');
  });

  it('renders Container for container scope (0.0% and nonzero) and dash for unavailable or host/unknown scope', async () => {
    mockDashboardApis({
      state: 'connected',
      checkedAt: 1700000000,
      stale: false,
      cpuLoad: 14,
      cpuCount: 4,
      cpuFrequencyMhz: 2400,
      cpuAvg: 11,
      cpuPeak: 22,
      cpuHistory: [10, 14],
      memHistory: [],
    });

    // Container scope + valid zero CPU -> 0.0%
    setMockStatus(
      new Status({
        resourceScope: 'container',
        unavailableMetrics: [],
        cpu: 0,
        mem: { current: 100, total: 1000 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    const zeroView = renderIndex();
    expect((await screen.findAllByText('Container')).length).toBe(2);
    let cpuCard = zeroView.container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.querySelector('.ov-tile-sub-val')?.textContent).toBe('0.0%');
    // Placed between primary value (.ov-tile-value) and hardware line (.ov-tile-detail)
    const children = Array.from(cpuCard.querySelector('.ant-card-body')?.children ?? []).map(
      (el) => el.className,
    );
    expect(children.indexOf('ov-tile-sub')).toBeGreaterThan(children.indexOf('ov-tile-value'));
    expect(children.indexOf('ov-tile-sub')).toBeLessThan(children.indexOf('ov-tile-detail'));
    expect(cpuCard.textContent).toContain('14.0%');
    expect(cpuCard.textContent).toContain('4 logical CPUs · 2.40 GHz');

    // Container scope + nonzero CPU -> 1.3%
    cleanup();
    setMockStatus(
      new Status({
        resourceScope: 'container',
        unavailableMetrics: [],
        cpu: 1.3,
        mem: { current: 100, total: 1000 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    const nonZeroView = renderIndex();
    expect(await screen.findByText('1.3%')).toBeInTheDocument();
    cpuCard = nonZeroView.container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.querySelector('.ov-tile-sub-val')?.textContent).toBe('1.3%');

    // Container scope + unavailable CPU -> —
    cleanup();
    setMockStatus(
      new Status({
        resourceScope: 'container',
        unavailableMetrics: ['cpu'],
        cpu: 55,
        mem: { current: 100, total: 1000 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    const unavailView = renderIndex();
    expect((await screen.findAllByText('Container')).length).toBe(2);
    cpuCard = unavailView.container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.querySelector('.ov-tile-sub-val')?.textContent).toBe('—');
    expect(cpuCard.textContent).not.toContain('55.0%');

    // Host/unknown scope -> — (never renders host fallback number as Container CPU)
    cleanup();
    setMockStatus(
      new Status({
        resourceScope: '',
        unavailableMetrics: [],
        cpu: 88.8,
        mem: { current: 100, total: 1000 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    const hostView = renderIndex();
    expect((await screen.findAllByText('Container')).length).toBe(2);
    cpuCard = hostView.container.querySelectorAll('.ov-vitals > *')[0];
    expect(cpuCard.querySelector('.ov-tile-sub-val')?.textContent).toBe('—');
    expect(cpuCard.textContent).not.toContain('88.8%');
  });
});
