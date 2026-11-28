import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';

import { AllSetting } from '@/models/setting';
import CertificatesPane from '@/pages/settings/CertificatesPane';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('CertificatesPane', () => {
  it('issues a separate Subscription Only certificate without overwriting Panel cert paths', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: true,
      msg: '',
      obj: {
        toolInstalled: true,
        routerosReady: true,
        managedNatEnabled: true,
        webCertFile: '/root/cert/panel.example.com/fullchain.pem',
        webKeyFile: '/root/cert/panel.example.com/privkey.pem',
        subCertFile: '/root/cert/panel.example.com/fullchain.pem',
        subKeyFile: '/root/cert/panel.example.com/privkey.pem',
        domains: [
          {
            domain: 'panel.example.com',
            mode: 'http-01',
            expiry: '2027-02-16T00:00:00Z',
            installed: true,
            autoRenew: false,
            tokenConfigured: false,
            certPath: '/root/cert/panel.example.com/fullchain.pem',
            keyPath: '/root/cert/panel.example.com/privkey.pem',
          },
        ],
      },
    });
    const postSpy = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
      success: true,
      msg: 'ok',
      obj: {
        domain: 'sub.example.com',
        mode: 'http-01',
        target: 'sub',
        certPath: '/root/cert/sub.example.com/fullchain.pem',
        keyPath: '/root/cert/sub.example.com/privkey.pem',
        notAfter: 1800000000,
        port80AutoManaged: true,
        port80Closed: true,
      },
    });
    const updateSetting = vi.fn();

    renderWithProviders(
      <CertificatesPane
        allSetting={
          new AllSetting({
            webCertFile: '/root/cert/panel.example.com/fullchain.pem',
            webKeyFile: '/root/cert/panel.example.com/privkey.pem',
            subCertFile: '/root/cert/panel.example.com/fullchain.pem',
            subKeyFile: '/root/cert/panel.example.com/privkey.pem',
          })
        }
        updateSetting={updateSetting}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText('panel.example.com')).toBeInTheDocument();
    });
    expect(screen.getByText('Panel + Sub')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Subscription Only'));
    fireEvent.change(screen.getByLabelText('Domain Name'), {
      target: { value: 'sub.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Issue Certificate' }));

    await waitFor(() => {
      expect(postSpy).toHaveBeenCalledWith(
        '/panel/api/setting/acmeIssue',
        expect.objectContaining({
          domain: 'sub.example.com',
          mode: 'http-01',
          target: 'sub',
          autoOpenPort80: true,
        }),
        { silent: true },
      );
    });

    expect(updateSetting).toHaveBeenCalledWith({
      subCertFile: '/root/cert/sub.example.com/fullchain.pem',
      subKeyFile: '/root/cert/sub.example.com/privkey.pem',
    });
  });

  it('displays separate Panel and Sub badges when two distinct certificates are active', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: true,
      msg: '',
      obj: {
        toolInstalled: true,
        routerosReady: false,
        managedNatEnabled: false,
        webCertFile: '/root/cert/panel.example.com/fullchain.pem',
        webKeyFile: '/root/cert/panel.example.com/privkey.pem',
        subCertFile: '/root/cert/sub.example.com/fullchain.pem',
        subKeyFile: '/root/cert/sub.example.com/privkey.pem',
        domains: [
          {
            domain: 'panel.example.com',
            mode: 'dns-01',
            expiry: '2027-02-16T00:00:00Z',
            installed: true,
            autoRenew: true,
            tokenConfigured: true,
            certPath: '/root/cert/panel.example.com/fullchain.pem',
            keyPath: '/root/cert/panel.example.com/privkey.pem',
          },
          {
            domain: 'sub.example.com',
            mode: 'dns-01',
            expiry: '2027-03-16T00:00:00Z',
            installed: true,
            autoRenew: true,
            tokenConfigured: true,
            certPath: '/root/cert/sub.example.com/fullchain.pem',
            keyPath: '/root/cert/sub.example.com/privkey.pem',
          },
        ],
      },
    });

    renderWithProviders(
      <CertificatesPane
        allSetting={
          new AllSetting({
            webCertFile: '/root/cert/panel.example.com/fullchain.pem',
            webKeyFile: '/root/cert/panel.example.com/privkey.pem',
            subCertFile: '/root/cert/sub.example.com/fullchain.pem',
            subKeyFile: '/root/cert/sub.example.com/privkey.pem',
          })
        }
        updateSetting={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText('panel.example.com')).toBeInTheDocument();
      expect(screen.getByText('sub.example.com')).toBeInTheDocument();
    });
    expect(screen.getByText('Panel')).toBeInTheDocument();
    expect(screen.getByText('Sub')).toBeInTheDocument();
  });
});
