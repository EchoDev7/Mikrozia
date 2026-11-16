import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

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
        onStopXray={noop}
        onRestartXray={noop}
        onOpenLogs={noop}
        onOpenXrayLogs={noop}
        onOpenAmneziaWGLogs={noop}
        onOpenConfig={noop}
        onOpenBackup={noop}
        onOpenSystemHistory={noop}
        onOpenXrayMetrics={noop}
      />,
    );

    expect(screen.getByText('v1-mikrozia')).toBeDefined();
    expect(screen.getByText('v26.10.21')).toBeDefined();
    // Xray version must not be inside an interactive button
    expect(screen.queryByRole('button', { name: /v26\.10\.21/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /update/i })).toBeNull();
  });
});
