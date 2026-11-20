import { Fragment, useId } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Dropdown, Modal, Tag, Tooltip } from 'antd';
import {
  ApiOutlined,
  ArrowUpOutlined,
  AreaChartOutlined,
  BarsOutlined,
  CloudServerOutlined,
  ControlOutlined,
  DownOutlined,
  FileTextOutlined,
  PoweroffOutlined,
  ReloadOutlined,
} from '@ant-design/icons';

import { formatPanelVersion } from '@/lib/panel-version';
import type { Status } from '@/models/status';

interface OverviewActionBarProps {
  status: Status;
  isMobile: boolean;
  accessLogEnable: boolean;
  panelVersion: string;
  routerOSStatus: { state: string; checkedAt: number; stale: boolean };
  onStopXray: () => void;
  onRestartXray: () => void;
  onRestartPanel: () => Promise<void>;
  onOpenLogs: () => void;
  onOpenXrayLogs: () => void;
  onOpenAmneziaWGLogs: () => void;
  onOpenConfig: () => void;
  onOpenBackup: () => void;
  onOpenSystemHistory: () => void;
  onOpenXrayMetrics: () => void;
}

interface BarAction {
  key: string;
  icon: ReactNode;
  text: string;
  onClick: () => void;
}

const XRAY_STATE_KEYS: Record<string, string> = {
  running: 'pages.index.xrayStatusRunning',
  stop: 'pages.index.xrayStatusStop',
  error: 'pages.index.xrayStatusError',
};

export default function OverviewActionBar({
  status,
  isMobile,
  accessLogEnable,
  panelVersion,
  routerOSStatus,
  onStopXray,
  onRestartXray,
  onRestartPanel,
  onOpenLogs,
  onOpenXrayLogs,
  onOpenAmneziaWGLogs,
  onOpenConfig,
  onOpenBackup,
  onOpenSystemHistory,
  onOpenXrayMetrics,
}: OverviewActionBarProps) {
  const { t } = useTranslation();
  const containerHintId = useId();
  const stopHintId = useId();
  const containerAccessibility = {
    'aria-label': t('pages.index.restartContainer'),
    'aria-describedby': containerHintId,
  };
  const stateText = t(XRAY_STATE_KEYS[status.xray.state] ?? 'pages.index.xrayStatusUnknown');
  const hasVersion = !!status.xray.version && status.xray.version !== 'Unknown';
  const size = isMobile ? ('small' as const) : ('middle' as const);
  const confirmXray = (operation: 'restart' | 'stop') => {
    const stopping = operation === 'stop';
    Modal.confirm({
      title: t(
        stopping ? 'pages.index.stopXrayConfirmTitle' : 'pages.index.restartXrayConfirmTitle',
      ),
      content: t(
        stopping ? 'pages.index.stopXrayConfirmDesc' : 'pages.index.restartXrayConfirmDesc',
      ),
      okText: t(stopping ? 'pages.index.stopXray' : 'pages.index.restartXray'),
      cancelText: t('cancel'),
      okButtonProps: stopping ? { danger: true } : undefined,
      onOk: stopping ? onStopXray : onRestartXray,
    });
  };
  const confirmPanel = () => {
    Modal.confirm({
      title: t('pages.index.restartPanelConfirmTitle'),
      content: t('pages.index.restartPanelConfirmDesc'),
      okText: t('pages.settings.restartPanel'),
      cancelText: t('cancel'),
      onOk: onRestartPanel,
    });
  };

  const routerOSStateKey: Record<string, string> = {
    not_configured: 'pages.index.routerosNotConfigured',
    invalid_configuration: 'pages.index.routerosInvalidConfig',
    connecting: 'pages.index.routerosConnecting',
    connected: 'pages.index.routerosConnected',
    authentication_failed: 'pages.index.routerosAuthenticationFailed',
    permission_denied: 'pages.index.routerosPermissionDenied',
    unreachable: 'pages.index.routerosUnreachable',
    query_failed: 'pages.index.routerosUnreachable',
    invalid_reply: 'pages.index.routerosUnreachable',
  };

  const actionGroups: BarAction[][] = [
    [
      {
        key: 'logs',
        icon: <BarsOutlined aria-hidden />,
        text: t('pages.index.logs'),
        onClick: onOpenLogs,
      },
      ...(accessLogEnable
        ? [
            {
              key: 'accessLogs',
              icon: <FileTextOutlined aria-hidden />,
              text: t('pages.index.accessLogs'),
              onClick: onOpenXrayLogs,
            },
          ]
        : []),
      ...(status.amneziawg.configured
        ? [
            {
              key: 'amneziawgLogs',
              icon: <ApiOutlined aria-hidden />,
              text: t('pages.index.amneziawgLogs'),
              onClick: onOpenAmneziaWGLogs,
            },
          ]
        : []),
      {
        key: 'config',
        icon: <ControlOutlined aria-hidden />,
        text: t('pages.index.xrayConfig'),
        onClick: onOpenConfig,
      },
      {
        key: 'backup',
        icon: <CloudServerOutlined aria-hidden />,
        text: t('pages.index.backupTitle'),
        onClick: onOpenBackup,
      },
    ],
    [
      {
        key: 'history',
        icon: <AreaChartOutlined aria-hidden />,
        text: t('pages.index.systemHistoryTitle'),
        onClick: onOpenSystemHistory,
      },
      {
        key: 'metrics',
        icon: <ArrowUpOutlined aria-hidden />,
        text: t('pages.index.xrayMetricsTitle'),
        onClick: onOpenXrayMetrics,
      },
    ],
  ];

  const statePill = (
    <span className="ov-state" data-state={status.xray.state}>
      <span className="ov-state-dot" style={{ color: status.xray.color }} />
      <span>{`${t('pages.index.xrayStatus')} · ${stateText}`}</span>
      {hasVersion && <span className="ov-state-version">{`v${status.xray.version}`}</span>}
    </span>
  );

  return (
    <div className="ov-bar">
      {status.xray.errorMsg ? (
        <Tooltip title={<span className="ov-error-detail">{status.xray.errorMsg}</span>}>
          {statePill}
        </Tooltip>
      ) : (
        statePill
      )}

      {status.xray.state === 'running' && status.xray.errorMsg ? (
        <Tooltip title={<span className="ov-error-detail">{status.xray.errorMsg}</span>}>
          <Tag color="error">{t('pages.index.xrayStatusError')}</Tag>
        </Tooltip>
      ) : null}

      <span className="ov-panel-version ov-mono">{formatPanelVersion(panelVersion)}</span>
      <Tooltip
        title={t(
          routerOSStatus.stale
            ? 'pages.index.routerosStale'
            : (routerOSStateKey[routerOSStatus.state] ?? 'pages.index.routerosUnreachable'),
        )}
      >
        <Tag
          color={
            routerOSStatus.state === 'connected' && !routerOSStatus.stale ? 'success' : 'default'
          }
        >
          {t('pages.index.routerosStatusPrefix')}:{' '}
          {t(
            routerOSStatus.stale
              ? 'pages.index.routerosStale'
              : (routerOSStateKey[routerOSStatus.state] ?? 'pages.index.routerosUnreachable'),
          )}
        </Tag>
      </Tooltip>

      <div className="ov-bar-actions">
        <Dropdown
          trigger={['click']}
          autoFocus
          menu={{
            items: [
              {
                key: 'xray',
                label: t('pages.index.restartXray'),
                onClick: () => confirmXray('restart'),
              },
              { key: 'panel', label: t('pages.settings.restartPanel'), onClick: confirmPanel },
              {
                key: 'container',
                disabled: true,
                ...containerAccessibility,
                label: (
                  <>
                    {t('pages.index.restartContainer')}
                    <div
                      id={containerHintId}
                      style={{ maxWidth: 230, whiteSpace: 'normal', fontSize: 12 }}
                    >
                      {t('pages.index.restartContainerUnavailable')}
                    </div>
                  </>
                ),
              },
            ],
          }}
        >
          <Button
            color="primary"
            variant="outlined"
            size={size}
            icon={<ReloadOutlined aria-hidden />}
            aria-label={t('pages.index.restartMenu')}
            aria-haspopup="menu"
          >
            {!isMobile && t('pages.index.restartMenu')}
            <DownOutlined aria-hidden />
          </Button>
        </Dropdown>
        <Dropdown
          trigger={['click']}
          autoFocus
          menu={{
            items: [
              {
                key: 'xray',
                label: t('pages.index.stopXray'),
                onClick: () => confirmXray('stop'),
              },
              ...(['Panel', 'Container'] as const).map((target) => {
                const label = t(`pages.index.stop${target}`);
                const hintId = `${stopHintId}-${target}`;
                return {
                  key: target,
                  disabled: true,
                  'aria-label': label,
                  'aria-describedby': hintId,
                  label: (
                    <>
                      {label}
                      <div
                        id={hintId}
                        style={{ maxWidth: 230, whiteSpace: 'normal', fontSize: 12 }}
                      >
                        {t(`pages.index.stop${target}Unavailable`)}
                      </div>
                    </>
                  ),
                };
              }),
            ],
          }}
        >
          <Button
            color="primary"
            variant="outlined"
            size={size}
            icon={<PoweroffOutlined aria-hidden />}
            aria-label={t('pages.index.stopMenu')}
            aria-haspopup="menu"
          >
            {!isMobile && t('pages.index.stopMenu')}
            <DownOutlined aria-hidden />
          </Button>
        </Dropdown>
        <span className="ov-bar-sep" />
        {actionGroups.map((group, groupIndex) => (
          <Fragment key={group[0].key}>
            {groupIndex > 0 && <span className="ov-bar-sep" />}
            {group.map((action) => (
              <Button
                key={action.key}
                type="text"
                size={size}
                icon={action.icon}
                aria-label={action.text}
                onClick={action.onClick}
              >
                {isMobile ? undefined : action.text}
              </Button>
            ))}
          </Fragment>
        ))}
      </div>
    </div>
  );
}
