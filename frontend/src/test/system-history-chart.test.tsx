import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import SystemHistoryModal from '@/pages/index/SystemHistoryModal';
import { Status } from '@/models/status';
import { renderWithProviders } from './test-utils';

const { get, plotData } = vi.hoisted(() => ({ get: vi.fn(), plotData: [] as unknown[][] }));

vi.mock('@/utils', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/utils')>();
  return { ...original, HttpUtil: { get } };
});

vi.mock('uplot', () => ({
  default: class {
    static paths = { spline: () => undefined };
    static pxRatio = 1;
    constructor(_opts: unknown, data: unknown[]) {
      plotData.push(data);
    }
    setData(data: unknown[]) {
      plotData.push(data);
    }
    setSize() {}
    redraw() {}
    destroy() {}
  },
}));

beforeEach(() => {
  get.mockReset();
  plotData.length = 0;
});

afterEach(() => {
  cleanup();
});

it('sorts history timestamps and preserves missing secondary samples as chart gaps', async () => {
  get.mockImplementation(async (path: string) => {
    if (path.includes('/tcpCount/')) {
      return {
        success: true,
        obj: [
          { t: 2, v: 22 },
          { t: 1, v: 11 },
        ],
      };
    }
    if (path.includes('/udpCount/')) {
      return {
        success: true,
        obj: [
          { t: 3, v: 33 },
          { t: 2, v: 20 },
        ],
      };
    }
    return { success: true, obj: [] };
  });

  renderWithProviders(<SystemHistoryModal open status={new Status()} onClose={() => {}} />);

  await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/server/history/containerCpu/2'));
  expect(screen.queryByRole('tab', { name: 'Connections' })).toBeNull();
  fireEvent.click(screen.getByRole('tab', { name: 'Container Sockets' }));
  expect(await screen.findByText('Container Sockets (TCP / UDP)')).toBeDefined();

  await waitFor(() => {
    const chart = plotData.at(-1);
    expect(chart).toEqual([
      [0, 1],
      [11, 22],
      [null, 20],
    ]);
  });
});

it('shows scope hints for RAM and Disk I/O while keeping all 6 tabs intact', async () => {
  get.mockResolvedValue({ success: true, obj: [] });

  renderWithProviders(<SystemHistoryModal open status={new Status()} onClose={() => {}} />);

  expect(screen.getAllByRole('tab').map((el) => el.textContent?.trim())).toEqual([
    'CPU',
    'RAM',
    'Container Sockets',
    'Disk I/O',
    'Disk Usage',
    'Online',
  ]);

  expect(screen.queryByRole('button', { name: /memory visible/i })).toBeNull();

  fireEvent.click(screen.getByRole('tab', { name: 'RAM' }));
  expect(await screen.findByText('Memory Usage')).toBeDefined();
  await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/server/history/containerMem/2'));
  const ramHint = screen.getByRole('button', {
    name: 'Shows cgroup-scoped container memory usage as a percentage of a finite limit. With no limit or no readable cgroup data, no sample is plotted.',
  });
  fireEvent.click(ramHint);
  expect(
    await screen.findByRole('tooltip', {
      name: 'Shows cgroup-scoped container memory usage as a percentage of a finite limit. With no limit or no readable cgroup data, no sample is plotted.',
    }),
  ).toBeDefined();

  fireEvent.click(screen.getByRole('tab', { name: 'Disk I/O' }));
  expect(
    await screen.findByText('Disk I/O', { selector: '.history-chart-title span' }),
  ).toBeDefined();
  const diskHint = screen.getByRole('button', {
    name: 'Shows kernel-reported disk activity. In a container, it can reflect shared host storage or be unavailable.',
  });
  fireEvent.click(diskHint);
  expect(
    await screen.findByRole('tooltip', {
      name: 'Shows kernel-reported disk activity. In a container, it can reflect shared host storage or be unavailable.',
    }),
  ).toBeDefined();
});
