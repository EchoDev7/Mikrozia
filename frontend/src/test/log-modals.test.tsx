import { cleanup, screen, waitFor } from '@testing-library/react';
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
  it('removes SysLog checkbox, posts only level, and shows RouterOS container guidance', async () => {
    const postSpy = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
      success: true,
      msg: '',
      obj: ['2026/06/09 00:35:09 INFO - panel started'],
    });

    renderWithProviders(<LogModal open onClose={() => {}} />);

    await waitFor(() => {
      expect(postSpy).toHaveBeenCalledTimes(1);
    });
    expect(postSpy).toHaveBeenCalledWith('/panel/api/server/logs/20', { level: 'info' });

    expect(screen.queryByText('SysLog')).toBeNull();
    expect(screen.queryByRole('checkbox', { name: /syslog/i })).toBeNull();

    expect(screen.getByText('RouterOS Container Logs (stdout/stderr)')).toBeDefined();
    expect(
      screen.getByText(
        /Mikrozia does not run these commands or change RouterOS logging automatically\./,
      ),
    ).toBeDefined();

    const setCmd =
      '/container/set [find where comment="[Mikrozia:container] Container"] logging=yes';
    const printCmd = '/log/print follow-only where topics~"container"';
    const pre = screen.getByText((_, node) => node?.tagName === 'PRE');
    expect(pre.getAttribute('dir')).toBe('ltr');
    expect(pre.textContent).toContain(setCmd);
    expect(pre.textContent).toContain(printCmd);
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
