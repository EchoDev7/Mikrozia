import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { AllSetting } from '@/models/setting';
import SecurityTab from '@/pages/settings/SecurityTab';
import { HttpUtil } from '@/utils';

describe('API token creation date', () => {
  it('renders both API seconds and legacy millisecond timestamps', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValueOnce({
      success: true,
      msg: '',
      obj: [
        {
          id: 2,
          name: 'seconds-token',
          enabled: true,
          createdAt: 1782485394,
          scope: 'node-sync',
          expiresAt: 0,
        },
        {
          id: 3,
          name: 'legacy-milliseconds-token',
          enabled: true,
          createdAt: 1782485394270,
          scope: 'node-sync',
          expiresAt: 0,
        },
      ],
    });

    render(
      <SecurityTab allSetting={{} as AllSetting} updateSetting={vi.fn()} saveSetting={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole('tab', { name: /API Token/ }));

    expect(await screen.findByText('seconds-token')).toBeTruthy();
    expect(screen.getByText('legacy-milliseconds-token')).toBeTruthy();
    expect(screen.getAllByText(/2026/)).toHaveLength(2);
    expect(screen.getAllByText('Node synchronization').length).toBeGreaterThanOrEqual(1);
  });

  it('creates a token with the selected scope', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, msg: '', obj: [] });
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
      success: true,
      msg: '',
      obj: { token: 'created-token' },
    });

    render(
      <SecurityTab allSetting={{} as AllSetting} updateSetting={vi.fn()} saveSetting={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole('tab', { name: /API Token/ }));
    fireEvent.click(await screen.findByRole('button', { name: /New token/ }));
    fireEvent.change(screen.getByPlaceholderText('e.g. central-panel-a'), {
      target: { value: 'central-node' },
    });

    const scope = screen.getByRole('combobox', { name: 'Scope' });
    fireEvent.mouseDown(scope.closest('.ant-select') ?? scope);
    const option = Array.from(document.querySelectorAll('.ant-select-item-option')).find(
      (item) => item.textContent === 'Node synchronization',
    );
    if (!option) throw new Error('Node synchronization option not found');
    fireEvent.click(option);
    expect(screen.getByText(/Synchronizes node configuration/)).toBeTruthy();

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/panel/api/setting/apiTokens/create', {
        name: 'central-node',
        scope: 'node-sync',
      }),
    );
  });
});
