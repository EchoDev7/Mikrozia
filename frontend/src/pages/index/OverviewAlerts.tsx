import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from 'antd';
import {
  CloudDownloadOutlined,
  ExclamationCircleOutlined,
  InfoCircleOutlined,
  WarningOutlined,
} from '@ant-design/icons';

import { USAGE_CRIT_PERCENT, USAGE_WARN_PERCENT, type Status } from '@/models/status';
import type { RouterOSAPIStatus } from '@/api/queries/useRouterOSAPIStatus';

export interface OverviewAlertItem {
  key: string;
  level: 'error' | 'warning' | 'info';
  text: string;
  actionLabel?: string;
  onAction?: () => void;
}

interface OverviewAlertsProps {
  status: Status;
  routerosStatus?: RouterOSAPIStatus | null;
  updateAvailable: boolean;
  latestVersion: string;
  onNavigate: (to: string) => void;
}

const SSL_EXPIRY_ALERT_DAYS = 14;
const CLOCK_SKEW_ALERT_SECONDS = 60;
const ROUTEROS_ERROR_STATES = new Set([
  'unreachable',
  'authentication_failed',
  'permission_denied',
  'query_failed',
  'invalid_reply',
  'invalid_configuration',
]);

export default function OverviewAlerts({
  status,
  routerosStatus,
  updateAvailable,
  latestVersion,
  onNavigate,
}: OverviewAlertsProps) {
  const { t } = useTranslation();

  const alerts = useMemo<OverviewAlertItem[]>(() => {
    const out: OverviewAlertItem[] = [];

    // 1. Resource pressure (RouterOS CPU/RAM when connected, plus Storage)
    const rosConnected = routerosStatus?.state === 'connected';
    const cpuPct =
      rosConnected && routerosStatus?.cpuLoad != null ? routerosStatus.cpuLoad : status.cpu.percent;
    const ramPct =
      rosConnected && routerosStatus?.memPercent != null
        ? routerosStatus.memPercent
        : status.mem.percent;
    const resources = [
      { name: t('pages.index.cpu'), value: cpuPct },
      { name: t('pages.index.memory'), value: ramPct },
      { name: t('pages.index.storage'), value: status.disk.percent },
    ];
    const fmtList = (xs: typeof resources) =>
      xs.map((i) => `${i.name} ${i.value.toFixed(0)}%`).join(', ');
    const crit = resources.filter((i) => i.value >= USAGE_CRIT_PERCENT);
    const warm = resources.filter(
      (i) => i.value >= USAGE_WARN_PERCENT && i.value < USAGE_CRIT_PERCENT,
    );
    if (crit.length > 0) {
      out.push({
        key: 'resource-crit',
        level: 'error',
        text: t('pages.index.healthCritical', { list: fmtList(crit) }),
      });
    } else if (warm.length > 0) {
      out.push({
        key: 'resource-warm',
        level: 'warning',
        text: t('pages.index.healthWarm', { list: fmtList(warm) }),
      });
    }

    // 2. RouterOS API disconnected / error (only when configured)
    if (routerosStatus?.state && ROUTEROS_ERROR_STATES.has(routerosStatus.state)) {
      out.push({
        key: 'routeros-disconnected',
        level: 'error',
        text: t('pages.index.alertRouterosDisconnected', { state: routerosStatus.state }),
        actionLabel: t('pages.index.alertActionSettings'),
        onAction: () => onNavigate('/settings#routeros-nat'),
      });
    }

    // 3. Managed NAT drift
    const nat = rosConnected ? routerosStatus?.managedNat : undefined;
    if (nat?.enabled && nat.state === 'drift') {
      const parts: string[] = [];
      if (nat.missingCount) {
        parts.push(t('pages.index.natMissingCount', { count: nat.missingCount }));
      }
      if (nat.orphanCount) {
        parts.push(t('pages.index.natOrphanCount', { count: nat.orphanCount }));
      }
      out.push({
        key: 'nat-drift',
        level: 'warning',
        text: t('pages.index.alertNatDrift', {
          detail: parts.join(' · ') || t('pages.index.natDrift'),
        }),
        actionLabel: t('pages.index.alertActionReviewNat'),
        onAction: () => onNavigate('/settings?sub=rules#routeros-nat'),
      });
    }

    // 4. SSL Certificate expired or expiring within 14 days
    const tls = status.tlsCert;
    if (tls?.configured && tls.notAfter) {
      const daysLeft = tls.daysLeft ?? 0;
      if (tls.expired || daysLeft <= 0) {
        out.push({
          key: 'ssl-expired',
          level: 'error',
          text: t('pages.index.alertSslExpired'),
          actionLabel: t('pages.index.alertActionRenewSsl'),
          onAction: () => onNavigate('/settings?sub=certs#general'),
        });
      } else if (daysLeft <= SSL_EXPIRY_ALERT_DAYS) {
        out.push({
          key: 'ssl-expiring',
          level: 'warning',
          text: t('pages.index.alertSslExpiringSoon', { days: daysLeft }),
          actionLabel: t('pages.index.alertActionRenewSsl'),
          onAction: () => onNavigate('/settings?sub=certs#general'),
        });
      }

      // 5. Port 80 left open on RouterOS while SSL is active
      if (!tls.expired && daysLeft > 0 && tls.port80Status === 'open') {
        out.push({
          key: 'port80-open',
          level: 'warning',
          text: t('pages.index.alertPort80OpenWithSsl'),
          actionLabel: t('pages.index.alertActionSettings'),
          onAction: () => onNavigate('/settings?sub=certs#general'),
        });
      }
    }

    // 6. Router/container clock skew (> 60s vs real time)
    if (status.clockSkewSeconds > CLOCK_SKEW_ALERT_SECONDS) {
      out.push({
        key: 'clock-skew',
        level: 'warning',
        text: t('pages.index.alertClockSkew', { seconds: status.clockSkewSeconds }),
      });
    }

    // 7. Newer Mikrozia image version available
    if (updateAvailable && latestVersion) {
      out.push({
        key: 'update-available',
        level: 'info',
        text: t('pages.index.alertUpdateAvailable', { version: latestVersion }),
        actionLabel: t('pages.index.alertActionUpdate'),
        onAction: () => onNavigate('/settings?sub=update#routeros-nat'),
      });
    }

    return out;
  }, [status, routerosStatus, updateAvailable, latestVersion, onNavigate, t]);

  if (alerts.length === 0) return null;

  return (
    <div className="ov-alerts" role="region" aria-label={t('menu.dashboard')}>
      {alerts.map((item) => (
        <div key={item.key} className={`ov-alert-item ov-alert-item--${item.level}`}>
          <span className="ov-alert-icon" aria-hidden>
            {item.level === 'error' ? (
              <ExclamationCircleOutlined aria-hidden />
            ) : item.level === 'warning' ? (
              <WarningOutlined aria-hidden />
            ) : item.key === 'update-available' ? (
              <CloudDownloadOutlined aria-hidden />
            ) : (
              <InfoCircleOutlined aria-hidden />
            )}
          </span>
          <span className="ov-alert-text">{item.text}</span>
          {item.actionLabel && item.onAction && (
            <Button
              size="small"
              type={item.level === 'info' ? 'primary' : 'default'}
              className="ov-alert-btn"
              onClick={item.onAction}
            >
              {item.actionLabel}
            </Button>
          )}
        </div>
      ))}
    </div>
  );
}
