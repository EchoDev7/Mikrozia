import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import LogModal from '@/pages/index/LogModal';
import XrayLogModal from '@/pages/index/XrayLogModal';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('LogModal', () => {
  it('loads Panel logs initially and reads only the verified RouterOS Container logs on its tab', async () => {
    const getSpy = vi.spyOn(HttpUtil, 'get').mockImplementation(async (url) => {
      if (url === '/panel/api/server/routerosAPIStatus') {
        return {
          success: true,
          msg: '',
          obj: {
            state: 'connected',
            checkedAt: 1,
            stale: false,
            cpuLoad: 0,
            cpuCount: 1,
            cpuFrequencyMhz: 1000,
            cpuAvg: 0,
            cpuPeak: 0,
            cpuHistory: [],
            memUsed: null,
            memTotal: null,
            memPercent: null,
            memAvg: null,
            memPeak: null,
            memHistory: [],
          },
        };
      }
      if (url === '/panel/api/server/routerosContainerLogs/20') {
        return {
          success: true,
          msg: '',
          obj: {
            state: 'connected',
            entries: [{ time: '2026-11-21 03:00:00', message: 'container started' }],
          },
        };
      }
      throw new Error(`unexpected GET ${url}`);
    });
    const postSpy = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => {
      if (url.includes('xrayRuntimeLogs')) {
        return {
          success: true,
          msg: '',
          obj: ['2026/06/09 00:35:09 WARNING - core: Xray 26.6.1 started'],
        };
      }
      return {
        success: true,
        msg: '',
        obj: ['2026/06/09 00:35:09 INFO - panel started'],
      };
    });

    renderWithProviders(<LogModal open onClose={() => {}} />);

    await waitFor(() => {
      expect(postSpy).toHaveBeenCalledTimes(1);
    });
    expect(postSpy).toHaveBeenCalledWith('/panel/api/server/logs/20', { level: 'info' });

    expect(screen.queryByText('SysLog')).toBeNull();
    expect(screen.queryByRole('checkbox', { name: /syslog/i })).toBeNull();

    expect(
      screen.getByText('Only in-memory Mikrozia panel application entries are shown.'),
    ).toBeDefined();
    expect(screen.getByText('X-UI:')).toBeDefined();
    expect(screen.queryByText(/stdout\/stderr/)).toBeNull();
    expect(screen.queryByText(/\/container\/set/)).toBeNull();

    fireEvent.click(screen.getByRole('tab', { name: 'RouterOS Container' }));
    expect(await screen.findByText('container started')).toBeDefined();
    expect(getSpy).toHaveBeenCalledWith('/panel/api/server/routerosContainerLogs/20', undefined, {
      silent: true,
    });
    expect(postSpy).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('tab', { name: 'Xray' }));
    expect(await screen.findByText('Xray Runtime Logs')).toBeDefined();
    await waitFor(() =>
      expect(postSpy).toHaveBeenCalledWith('/panel/api/server/xrayRuntimeLogs/20', {
        level: 'info',
      }),
    );
    expect(
      await screen.findByText(
        'This tab shows in-memory Xray engine runtime messages. It is different from Xray Access Logs, which record proxy traffic.',
      ),
    ).toBeDefined();
    expect(screen.queryByText(/Access logs may record client source IP addresses/i)).toBeNull();
    expect(await screen.findByText('XRAY:')).toBeDefined();
    expect(screen.queryByText('X-UI:')).toBeNull();
  });
});

describe('XrayLogModal', () => {
  it('renders privacy notice and description for Xray access logs', async () => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue({
      success: true,
      msg: '',
      obj: [],
    });

    renderWithProviders(<XrayLogModal open onClose={() => {}} />);

    await waitFor(() => {
      expect(screen.getByText('Privacy Notice: Xray Access Logs')).toBeDefined();
    });
    expect(
      screen.getByText(
        /Access logs may record client source IP addresses, destination addresses, and account emails\./,
      ),
    ).toBeDefined();
  });
});
