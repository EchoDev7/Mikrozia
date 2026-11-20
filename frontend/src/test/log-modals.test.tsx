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
  it('loads only Panel logs initially and keeps RouterOS Container guidance truthful', async () => {
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
    expect(
      await screen.findByText(/no stable configured identity for the Mikrozia container/i),
    ).toBeDefined();
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
