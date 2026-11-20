import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { Status } from '@/models/status';
import ThroughputCard from '@/pages/index/ThroughputCard';
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

afterEach(() => {
  cleanup();
});

describe('ThroughputCard', () => {
  it('renders genuine zero traffic as 0 B/s when netIO is available', () => {
    renderWithProviders(
      <ThroughputCard
        status={new Status({ netIO: { up: 0, down: 0 }, netTraffic: { sent: 0, recv: 0 } })}
        up={[0, 0]}
        down={[0, 0]}
        labels={['12:00:00', '12:00:02']}
        isMobile={false}
      />,
    );

    expect(screen.getByText('Overall Speed')).toBeDefined();
    expect(screen.getAllByText('0 B/s').length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText('0 B').length).toBe(2);
    expect(screen.queryByText('Unavailable')).toBeNull();
  });

  it('renders explicit Unavailable state when netIO is in unavailableMetrics', () => {
    renderWithProviders(
      <ThroughputCard
        status={
          new Status({
            unavailableMetrics: ['netIO'],
            netIO: { up: 0, down: 0 },
            netTraffic: { sent: 0, recv: 0 },
          })
        }
        up={[null, null]}
        down={[null, null]}
        labels={['12:00:00', '12:00:02']}
        isMobile={false}
      />,
    );

    expect(screen.getAllByText('Unavailable').length).toBe(2);
    expect(screen.queryByText('0 B/s')).toBeNull();
    expect(screen.getByText(/peak —/i)).toBeDefined();
  });

  it('renders normal traffic rates, totals, window averages, and scope hint', async () => {
    renderWithProviders(
      <ThroughputCard
        status={
          new Status({
            netIO: { up: 1024, down: 2048 },
            netTraffic: { sent: 4096, recv: 8192 },
          })
        }
        up={[1024, 2048]}
        down={[2048, 4096]}
        labels={['12:00:00', '12:00:02']}
        isMobile={false}
      />,
    );

    expect(screen.getByText('1.00 KB/s')).toBeDefined();
    expect(screen.getByText('2.00 KB/s')).toBeDefined();
    expect(screen.getByText('4.00 KB')).toBeDefined();
    expect(screen.getByText('8.00 KB')).toBeDefined();
    expect(screen.getByText('↑ 1.50 KB/s')).toBeDefined();
    expect(screen.getByText('↓ 3.00 KB/s')).toBeDefined();
    expect(screen.getByText(/peak 4\.00 KB\/s/i)).toBeDefined();

    const hintBtn = screen.getByRole('button', {
      name: /traffic visible from the container network interface/i,
    });
    fireEvent.click(hintBtn);
    expect(
      await screen.findByRole('tooltip', {
        name: /traffic visible from the container network interface/i,
      }),
    ).toBeDefined();
  });
});
