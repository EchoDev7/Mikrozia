import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { formatPanelVersion } from '@/lib/panel-version';

import { Status } from '@/models/status';
import OverviewActionBar from '@/pages/index/OverviewActionBar';

import { renderWithProviders } from './test-utils';

const noop = () => {};

describe('OverviewActionBar version display', () => {
  it('renders panel and xray version as read-only text and has no update button', () => {
    const status = new Status();
    status.xray.version = '26.10.21';

    renderWithProviders(
      <OverviewActionBar
        status={status}
        isMobile={false}
        accessLogEnable={false}
        panelVersion="v1-mikrozia"
        routerOSStatus={{ state: 'connecting', checkedAt: 0, stale: false }}
        onStopXray={noop}
        onRestartXray={noop}
        onRestartPanel={async () => {}}
        onOpenLogs={noop}
        onOpenXrayLogs={noop}
        onOpenAmneziaWGLogs={noop}
        onOpenConfig={noop}
        onOpenBackup={noop}
        onOpenSystemHistory={noop}
        onOpenXrayMetrics={noop}
      />,
    );

    expect(screen.getByText('Mikrozia v1')).toBeDefined();
    expect(screen.getByText('v26.10.21')).toBeDefined();
    // Xray version must not be inside an interactive button
    expect(screen.queryByRole('button', { name: /v26\.10\.21/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /update/i })).toBeNull();
  });

  it('requires explicit confirmation for Xray restart and stop, and cancel does not run either action', async () => {
    const restart = vi.fn();
    const stop = vi.fn();
    renderWithProviders(
      <OverviewActionBar
        status={new Status()}
        isMobile={false}
        accessLogEnable={false}
        panelVersion="v1.0.1-mikrozia"
        routerOSStatus={{ state: 'connected', checkedAt: 1, stale: false }}
        onStopXray={stop}
        onRestartXray={restart}
        onRestartPanel={async () => {}}
        onOpenLogs={noop}
        onOpenXrayLogs={noop}
        onOpenAmneziaWGLogs={noop}
        onOpenConfig={noop}
        onOpenBackup={noop}
        onOpenSystemHistory={noop}
        onOpenXrayMetrics={noop}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Restart' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Restart Xray' }));
    expect(
      await screen.findByText(/active Xray connections will briefly disconnect/i),
    ).toBeDefined();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(restart).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Stop Xray' }));
    const warning = await screen.findByText(
      /all Xray inbounds and connected users will be disconnected/i,
    );
    fireEvent.click(
      within(warning.closest<HTMLElement>('[role="dialog"]')!).getByRole('button', {
        name: 'Stop Xray',
      }),
    );
    await waitFor(() => expect(stop).toHaveBeenCalledTimes(1));
    expect(restart).not.toHaveBeenCalled();
  });

  it('presents canonical, historical, and development versions truthfully', () => {
    expect(formatPanelVersion('v1.0.1-mikrozia')).toBe('Mikrozia v1.0.1');
    expect(formatPanelVersion('v1-mikrozia')).toBe('Mikrozia v1');
    expect(formatPanelVersion('dev')).toBe('Mikrozia dev');
    expect(formatPanelVersion('dev+abc123')).toBe('Mikrozia dev+abc123');
  });
});
