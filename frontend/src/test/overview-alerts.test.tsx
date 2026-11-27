import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { Status } from '@/models/status';
import OverviewAlerts from '@/pages/index/OverviewAlerts';
import { renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
});

function healthyStatus(overrides: ConstructorParameters<typeof Status>[0] = {}) {
  return new Status({
    cpu: 10,
    mem: { current: 200, total: 1000 },
    disk: { current: 100, total: 1000 },
    xray: { state: 'running', version: '26.10.21' },
    ...overrides,
  });
}

describe('OverviewAlerts', () => {
  it('renders nothing when everything is healthy', () => {
    const { container } = renderWithProviders(
      <OverviewAlerts
        status={healthyStatus({
          tlsCert: {
            configured: true,
            subject: 'panel.example.com',
            notAfter: Math.floor(Date.now() / 1000) + 60 * 86400,
            daysLeft: 60,
            expired: false,
            port80Status: 'closed',
          },
        })}
        routerosStatus={{
          state: 'connected',
          checkedAt: 1700000000,
          stale: false,
          cpuLoad: 12,
          memPercent: 35,
          cpuHistory: [],
          memHistory: [],
          managedNat: {
            enabled: true,
            state: 'in_sync',
            ruleCount: 4,
            missingCount: 0,
            orphanCount: 0,
          },
        }}
        updateAvailable={false}
        latestVersion=""
        onNavigate={vi.fn()}
      />,
    );

    expect(container.querySelector('.ov-alerts')).toBeNull();
  });

  it('shows actionable alerts for SSL expiring, Port 80 open, NAT drift, RouterOS error, and update available', () => {
    const onNavigate = vi.fn();

    renderWithProviders(
      <OverviewAlerts
        status={healthyStatus({
          disk: { current: 920, total: 1000 },
          tlsCert: {
            configured: true,
            subject: 'panel.example.com',
            notAfter: Math.floor(Date.now() / 1000) + 7 * 86400,
            daysLeft: 7,
            expired: false,
            port80Status: 'open',
          },
        })}
        routerosStatus={{
          state: 'unreachable',
          checkedAt: 1700000000,
          stale: false,
          cpuHistory: [],
          memHistory: [],
        }}
        updateAvailable
        latestVersion="v2.0.5-mikrozia"
        onNavigate={onNavigate}
      />,
    );

    expect(screen.getByText(/Storage 92%/i)).toBeInTheDocument();
    expect(screen.getByText(/RouterOS API connection failed/i)).toBeInTheDocument();
    expect(screen.getByText(/SSL certificate expires in 7 days/i)).toBeInTheDocument();
    expect(screen.getByText(/Port 80 is still open on RouterOS/i)).toBeInTheDocument();
    expect(screen.getByText(/Mikrozia v2\.0\.5-mikrozia is available/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Update' }));
    expect(onNavigate).toHaveBeenCalledWith('/settings?sub=update#routeros-nat');
  });

  it('warns on router clock skew > 60s and stays silent within 60s', () => {
    const nowSec = Math.floor(Date.now() / 1000);
    const { rerender, container } = renderWithProviders(
      <OverviewAlerts
        status={healthyStatus({ serverTime: nowSec - 5 })}
        routerosStatus={null}
        updateAvailable={false}
        latestVersion=""
        onNavigate={vi.fn()}
      />,
    );
    expect(container.querySelector('.ov-alerts')).toBeNull();

    rerender(
      <OverviewAlerts
        status={healthyStatus({ serverTime: nowSec - 180 })}
        routerosStatus={null}
        updateAvailable={false}
        latestVersion=""
        onNavigate={vi.fn()}
      />,
    );
    expect(screen.getByText(/Router clock differs from real time/i)).toBeInTheDocument();
  });
});
