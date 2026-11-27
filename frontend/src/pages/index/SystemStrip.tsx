import { useTranslation } from 'react-i18next';
import { Card, Tag, Tooltip } from 'antd';
import {
  ClockCircleOutlined,
  CloudServerOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  GlobalOutlined,
} from '@ant-design/icons';

import { TimeFormatter } from '@/utils';
import { activateOnKey } from '@/utils/a11y';
import type { Status } from '@/models/status';
import type { RouterOSAPIStatus } from '@/api/queries/useRouterOSAPIStatus';

interface SystemStripProps {
  status: Status;
  routerosStatus?: RouterOSAPIStatus | null;
  showIp: boolean;
  onToggleIp: () => void;
  onOpenRouterOS?: () => void;
}

export default function SystemStrip({
  status,
  routerosStatus,
  showIp,
  onToggleIp,
  onOpenRouterOS,
}: SystemStripProps) {
  const { t } = useTranslation();
  const routerosConnected = routerosStatus?.state === 'connected';
  const routerosUptime = routerosConnected ? routerosStatus?.uptimeSeconds : null;
  const hasRouterosUptime = routerosUptime != null && routerosUptime >= 0;

  const board = routerosConnected ? routerosStatus?.boardName?.trim() : '';
  const arch = routerosConnected ? routerosStatus?.architectureName?.trim() : '';
  const routerModel =
    board && arch && board.toLowerCase() !== arch.toLowerCase()
      ? `${board} · ${arch}`
      : board || arch || '—';
  const routerVersion = routerosConnected ? routerosStatus?.version?.trim() : '';

  const nat = routerosConnected ? routerosStatus?.managedNat : undefined;
  const natDriftParts: string[] = [];
  if (nat?.missingCount) {
    natDriftParts.push(t('pages.index.natMissingCount', { count: nat.missingCount }));
  }
  if (nat?.orphanCount) {
    natDriftParts.push(t('pages.index.natOrphanCount', { count: nat.orphanCount }));
  }

  return (
    <Card hoverable styles={{ body: { padding: 0 } }}>
      <div className="ov-strip-grid">
        <div className="ov-strip-cell">
          {onOpenRouterOS ? (
            <div
              className="ov-kicker ov-kicker-icon ov-strip-link"
              role="button"
              tabIndex={0}
              onClick={onOpenRouterOS}
              onKeyDown={activateOnKey(onOpenRouterOS)}
            >
              <CloudServerOutlined aria-hidden />
              {t('pages.index.routerosNatHealth')}
            </div>
          ) : (
            <div className="ov-kicker ov-kicker-icon">
              <CloudServerOutlined aria-hidden />
              {t('pages.index.routerosNatHealth')}
            </div>
          )}
          <div className="ov-strip-split" style={{ flexWrap: 'wrap' }}>
            <div>
              <div className="ov-strip-sub">RouterOS</div>
              <div className="ov-strip-value ov-strip-value--compact">{routerModel}</div>
              <div className="ov-mono ov-ip-v6">
                {routerVersion
                  ? `v${routerVersion}`
                  : routerosStatus?.state === 'not_configured'
                    ? t('pages.index.routerosApiNotConfigured')
                    : '—'}
              </div>
            </div>
            <span className="ov-strip-split-sep" />
            <div>
              <div className="ov-strip-sub">{t('pages.index.managedNat')}</div>
              <div className="ov-nat-status-row">
                {!routerosConnected || !nat ? (
                  <span className="ov-strip-value ov-strip-value--compact">—</span>
                ) : nat.state === 'in_sync' ? (
                  <Tag color="success" style={{ marginInlineEnd: 0 }}>
                    {t('pages.index.natInSync')}
                  </Tag>
                ) : nat.state === 'drift' ? (
                  <Tooltip title={natDriftParts.join(' · ') || t('pages.index.natDrift')}>
                    <Tag color="warning" style={{ marginInlineEnd: 0 }}>
                      {t('pages.index.natDrift')}
                    </Tag>
                  </Tooltip>
                ) : nat.state === 'disabled' ? (
                  <Tag color="default" style={{ marginInlineEnd: 0 }}>
                    {t('pages.index.natDisabled')}
                  </Tag>
                ) : (
                  <Tag color="error" style={{ marginInlineEnd: 0 }}>
                    {t('pages.index.unavailable')}
                  </Tag>
                )}
              </div>
              {routerosConnected && nat && nat.enabled && (
                <div className="ov-mono ov-ip-v6">
                  {t('pages.index.natRulesCount', { count: nat.ruleCount })}
                  {natDriftParts.length > 0 ? ` · ${natDriftParts.join(' · ')}` : ''}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="ov-strip-cell">
          <div className="ov-kicker ov-kicker-icon">
            <ClockCircleOutlined aria-hidden />
            {t('pages.index.uptime')}
          </div>
          <div className="ov-strip-split">
            <div>
              <div className="ov-strip-sub">Xray</div>
              <div className="ov-strip-value">
                {TimeFormatter.formatSecond(status.appStats.uptime)}
              </div>
            </div>
            <span className="ov-strip-split-sep" />
            <div>
              <div className="ov-strip-sub">
                {hasRouterosUptime
                  ? 'RouterOS'
                  : status.resourceScope === 'container'
                    ? 'CT'
                    : 'OS'}
              </div>
              <div className="ov-strip-value">
                {hasRouterosUptime
                  ? TimeFormatter.formatSecond(routerosUptime)
                  : status.metricAvailable('uptime')
                    ? TimeFormatter.formatSecond(status.uptime)
                    : '—'}
              </div>
            </div>
          </div>
        </div>

        <div className="ov-strip-cell">
          <div className="ov-kicker ov-kicker-icon">
            <GlobalOutlined aria-hidden />
            {t('pages.index.ipAddresses')}
            <Tooltip title={t('pages.index.toggleIpVisibility')}>
              {showIp ? (
                <EyeOutlined
                  className="ip-toggle-icon"
                  role="button"
                  tabIndex={0}
                  aria-label={t('pages.index.toggleIpVisibility')}
                  onClick={onToggleIp}
                  onKeyDown={activateOnKey(onToggleIp)}
                />
              ) : (
                <EyeInvisibleOutlined
                  className="ip-toggle-icon"
                  role="button"
                  tabIndex={0}
                  aria-label={t('pages.index.toggleIpVisibility')}
                  onClick={onToggleIp}
                  onKeyDown={activateOnKey(onToggleIp)}
                />
              )}
            </Tooltip>
          </div>
          <div
            className={`ov-strip-split${showIp ? '' : ' ip-hidden'}`}
            style={{ flexWrap: 'wrap' }}
          >
            <div>
              <div className="ov-strip-sub">{t('pages.index.publicIp')}</div>
              <div className="ov-mono">{status.publicIP.ipv4}</div>
              {status.publicIP.ipv6 && status.publicIP.ipv6 !== 'N/A' && (
                <div className="ov-mono ov-ip-v6">{status.publicIP.ipv6}</div>
              )}
            </div>
            {(status.network?.containerIP || status.network?.containerGateway) && (
              <>
                <span className="ov-strip-split-sep" />
                <div>
                  <div className="ov-strip-sub">{t('pages.index.containerNetwork')}</div>
                  <div className="ov-mono">{status.network.containerIP || '—'}</div>
                  {status.network.containerGateway && (
                    <div className="ov-mono ov-ip-v6">
                      {t('pages.index.gateway')}: {status.network.containerGateway}
                    </div>
                  )}
                </div>
              </>
            )}
            {(status.network?.routerWANGateway || status.network?.routerWANIface) && (
              <>
                <span className="ov-strip-split-sep" />
                <div>
                  <div className="ov-strip-sub">{t('pages.index.routerosWan')}</div>
                  <div className="ov-mono">
                    {status.network.routerWANGateway || status.network.routerWANIface}
                  </div>
                  {status.network.routerWANIface && status.network.routerWANGateway && (
                    <div className="ov-mono ov-ip-v6">{status.network.routerWANIface}</div>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </Card>
  );
}
