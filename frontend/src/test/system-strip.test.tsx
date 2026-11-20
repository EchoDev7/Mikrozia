import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { Status } from '@/models/status';
import SystemStrip from '@/pages/index/SystemStrip';
import { renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
});

describe('SystemStrip', () => {
  it('renders only Uptime and IP Addresses without Panel RAM or Threads', () => {
    const status = new Status({
      resourceScope: 'container',
      uptime: 14400,
      appStats: { mem: 125829120, threads: 32, uptime: 95 },
      publicIP: { ipv4: '203.0.113.10', ipv6: '2001:db8::10' },
    });
    const onToggleIp = vi.fn();

    const { container } = renderWithProviders(
      <SystemStrip status={status} showIp={false} onToggleIp={onToggleIp} />,
    );

    const cells = container.querySelectorAll('.ov-strip-cell');
    expect(cells).toHaveLength(2);

    expect(screen.getByText('Uptime')).toBeInTheDocument();
    expect(screen.getByText('Xray')).toBeInTheDocument();
    expect(screen.getByText('CT')).toBeInTheDocument();
    expect(screen.getByText('IP Addresses')).toBeInTheDocument();
    expect(screen.getByText('203.0.113.10')).toBeInTheDocument();
    expect(screen.getByText('2001:db8::10')).toBeInTheDocument();

    expect(screen.queryByText('Panel')).toBeNull();
    expect(screen.queryByText('Threads')).toBeNull();
    expect(screen.queryByText('32')).toBeNull();
    expect(screen.queryByText('120.00 MB')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Toggle visibility of the IP' }));
    expect(onToggleIp).toHaveBeenCalledTimes(1);
  });
});
