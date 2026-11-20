import { cleanup, fireEvent, screen } from '@testing-library/react';
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
          cpu: 12,
          mem: { current: 999999999, total: 8000000000 },
          containerMem: null,
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
      cpu: 12,
      mem: { current: 999999999, total: 8000000000 },
      containerMem: null,
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

describe('Dashboard RAM card RouterOS & Container RAM wiring', () => {
  it('renders valid RouterOS 0.0% (0 B / total) and finite Container RAM without using status.mem', async () => {
    setMockStatus(
      new Status({
        resourceScope: 'container',
        mem: { current: 999999999, total: 8000000000 },
        containerMem: { used: 134217728, limit: 536870912 },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    mockDashboardApis({
      state: 'connected',
      checkedAt: 1700000000,
      stale: false,
      cpuLoad: 5,
      cpuCount: 2,
      cpuFrequencyMhz: 2400,
      cpuAvg: 5,
      cpuPeak: 5,
      cpuHistory: [5],
      memUsed: 0,
      memTotal: 1073741824,
      memPercent: 0,
      memAvg: 0,
      memPeak: 0,
      memHistory: [0],
    });

    const { container } = renderIndex();
    expect(await screen.findByText('0 B / 1.00 GB')).toBeInTheDocument();

    const ramCard = container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.textContent).toContain('0.0');
    expect(ramCard.textContent).toContain('0 B / 1.00 GB');
    expect(ramCard.textContent).toContain('Container');
    expect(ramCard.textContent).toContain('128.00 MB / 512.00 MB');
    expect(ramCard.textContent).not.toContain('953.67 MB');
    expect(ramCard.textContent).not.toContain('7.45 GB');

    const vitalsText = container.querySelector('.ov-vitals')?.textContent ?? '';
    expect(vitalsText).toContain('CPU');
    expect(vitalsText).toContain('Storage');
    expect(vitalsText).toContain('Clients');
    expect(vitalsText).toContain('Top Inbounds');
    expect(screen.getByText('Overall Speed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Xray Metrics' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  it('renders nonzero RouterOS RAM, stale badge, unlimited Container RAM, and unavailable Container RAM', async () => {
    setMockStatus(
      new Status({
        resourceScope: 'container',
        mem: { current: 999999999, total: 8000000000 },
        containerMem: { used: 67108864, limit: null },
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    mockDashboardApis({
      state: 'connected',
      checkedAt: 1700000000,
      stale: true,
      cpuLoad: 10,
      cpuCount: 2,
      cpuFrequencyMhz: null,
      cpuAvg: 10,
      cpuPeak: 10,
      cpuHistory: [10],
      memUsed: 536870912,
      memTotal: 2147483648,
      memPercent: 25,
      memAvg: 20,
      memPeak: 30,
      memHistory: [20, 25],
    });

    const { container } = renderIndex();
    expect(await screen.findByText('512.00 MB / 2.00 GB')).toBeInTheDocument();

    let ramCard = container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.textContent).toContain('25.0');
    expect(ramCard.textContent).toContain('Stale');
    expect(ramCard.textContent).toContain('64.00 MB · No limit');
    expect(ramCard.querySelector('.ov-tile-foot')?.textContent).toContain('20%');
    expect(ramCard.querySelector('.ov-tile-foot')?.textContent).toContain('30%');

    // Unavailable containerMem -> — (never status.mem)
    cleanup();
    setMockStatus(
      new Status({
        resourceScope: '',
        mem: { current: 999999999, total: 8000000000 },
        containerMem: null,
        disk: { current: 50, total: 1000 },
        xray: { state: 'running', version: '26.10.21' },
      }),
    );
    const unavailContainer = renderIndex();
    expect(await screen.findByText('512.00 MB / 2.00 GB')).toBeInTheDocument();
    ramCard = unavailContainer.container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.querySelector('.ov-tile-sub-val')?.textContent).toBe('—');
    expect(ramCard.textContent).not.toContain('7.45 GB');
  });

  it('renders not_configured, connecting, and Unavailable states cleanly without fake 0 B', async () => {
    mockDashboardApis({
      state: 'not_configured',
      checkedAt: 0,
      stale: false,
      cpuHistory: [],
      memHistory: [],
    });
    const notCfg = renderIndex();
    await screen.findByText('Clients');
    let ramCard = notCfg.container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.textContent).toContain('RouterOS API not configured');
    expect(ramCard.textContent).not.toContain('0 B');
    expect(ramCard.querySelector('.ov-tile-foot')?.textContent).toBe('——');

    cleanup();
    mockDashboardApis({
      state: 'connecting',
      checkedAt: 0,
      stale: false,
      cpuHistory: [],
      memHistory: [],
    });
    const conn = renderIndex();
    await screen.findByText('Clients');
    ramCard = conn.container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.querySelector('.ov-tile-number')?.textContent).toBe('—');
    expect(ramCard.textContent).not.toContain('0 B');

    cleanup();
    mockDashboardApis(null, true);
    const errView = renderIndex();
    await screen.findByText('Clients');
    ramCard = errView.container.querySelectorAll('.ov-vitals > *')[1];
    expect(ramCard.textContent).toContain('Unavailable');
    expect(ramCard.textContent).not.toContain('0 B');
  });
});
