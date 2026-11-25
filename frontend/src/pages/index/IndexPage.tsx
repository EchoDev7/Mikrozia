import { lazy, useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, ConfigProvider, Layout, Modal, Result, Spin, message } from 'antd';
import {
  CopyOutlined,
  CloudDownloadOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  HddOutlined,
} from '@ant-design/icons';

import {
  HttpUtil,
  CPUFormatter,
  SizeFormatter,
  ClipboardManager,
  FileManager,
  PromiseUtil,
} from '@/utils';
import { withBasePath } from '@/api/http-init';
import {
  USAGE_CRIT_COLOR,
  USAGE_CRIT_PERCENT,
  USAGE_WARN_COLOR,
  USAGE_WARN_PERCENT,
} from '@/models/status';
import { useTheme } from '@/hooks/useTheme';
import { useStatusQuery } from '@/api/queries/useStatusQuery';
import { useRouterOSAPIStatus } from '@/api/queries/useRouterOSAPIStatus';
import { useRouterOSContainerStatus } from '@/api/queries/useRouterOSContainerStatus';
import { ContainerRestartResultSchema } from '@/generated/zod';
import { useClientsSummary } from '@/api/queries/useClientsSummary';
import { useTopInbounds } from '@/api/queries/useTopInbounds';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import AppSidebar from '@/layouts/AppSidebar';
import { LazyMount } from '@/components/utility';
import SponsorSlot from '@/components/sponsor/SponsorSlot';
import { setMessageInstance } from '@/utils/messageBus';
import OverviewActionBar from './OverviewActionBar';
import VitalTile from './VitalTile';
import ClientsSummaryTile from './ClientsSummaryTile';
import TopInboundsTile from './TopInboundsTile';
import SslCertTile from './SslCertTile';
import ThroughputCard from './ThroughputCard';
import ConnectionsCard from './ConnectionsCard';
import SystemStrip from './SystemStrip';
import { mean, useOverviewHistory } from './useOverviewHistory';
import type { PanelUpdateInfo } from '@/generated/types';
const JsonEditor = lazy(() => import('@/components/form/JsonEditor'));
const LogModal = lazy(() => import('./LogModal'));
const BackupModal = lazy(() => import('./BackupModal'));
const SystemHistoryModal = lazy(() => import('./SystemHistoryModal'));
const XrayMetricsModal = lazy(() => import('./XrayMetricsModal'));
const XrayLogModal = lazy(() => import('./XrayLogModal'));
const AmneziaWGLogModal = lazy(() => import('./AmneziaWGLogModal'));
import './IndexPage.css';

export default function IndexPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { status, fetched, fetchError, refresh } = useStatusQuery();
  const routerOSAPIStatus = useRouterOSAPIStatus();
  const routerOSContainerStatus = useRouterOSContainerStatus();
  const { isMobile } = useMediaQuery();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const [accessLogEnable, setAccessLogEnable] = useState(false);
  const [panelUpdateInfo, setPanelUpdateInfo] = useState<PanelUpdateInfo>({
    channel: 'stable',
    currentVersion: '',
    latestVersion: '',
    updateAvailable: false,
  });

  const basePath = window.X_UI_BASE_PATH || '';

  const [showIp, setShowIp] = useState(false);
  const [logsOpen, setLogsOpen] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [sysHistoryOpen, setSysHistoryOpen] = useState(false);
  const [xrayMetricsOpen, setXrayMetricsOpen] = useState(false);
  const [xrayLogsOpen, setXrayLogsOpen] = useState(false);
  const [amneziawgLogsOpen, setAmneziawgLogsOpen] = useState(false);
  const [configTextOpen, setConfigTextOpen] = useState(false);
  const [configText, setConfigText] = useState('');
  const [loading, setLoading] = useState(false);
  const [loadingTip, setLoadingTip] = useState(t('loading'));

  const history = useOverviewHistory(status, fetched && !fetchError);
  const clientsSummary = useClientsSummary(fetched && !fetchError);
  const topInbounds = useTopInbounds(fetched && !fetchError);

  useEffect(() => {
    HttpUtil.post<{ accessLogEnable?: boolean }>('/panel/api/setting/defaultSettings').then(
      (msg) => {
        if (msg?.success && msg.obj) {
          setAccessLogEnable(!!msg.obj.accessLogEnable);
        }
      },
    );
    HttpUtil.get<PanelUpdateInfo>('/panel/api/server/getPanelUpdateInfo').then((msg) => {
      if (msg?.success && msg.obj) setPanelUpdateInfo(msg.obj);
    });
  }, []);

  const displayVersion = useMemo(
    () => window.X_UI_CUR_VER || panelUpdateInfo.currentVersion || '?',
    [panelUpdateInfo.currentVersion],
  );

  const setBusy = useCallback(({ busy, tip }: { busy: boolean; tip?: string }) => {
    setLoading(busy);
    if (tip) setLoadingTip(tip);
  }, []);

  const stopXray = useCallback(async () => {
    await HttpUtil.post('/panel/api/server/stopXrayService');
    await refresh();
  }, [refresh]);

  const restartXray = useCallback(async () => {
    await HttpUtil.post('/panel/api/server/restartXrayService');
    await refresh();
  }, [refresh]);

  const restartPanel = useCallback(async () => {
    try {
      const token = await HttpUtil.get<string>('/csrf-token', undefined, { silent: true });
      if (!token?.success || typeof token.obj !== 'string' || !token.obj) {
        throw new Error('Missing CSRF token');
      }
      // The shared POST helper can replay a 403; this operation must only be sent once.
      const response = await fetch(withBasePath('/panel/api/setting/restartPanel'), {
        method: 'POST',
        credentials: 'same-origin',
        redirect: 'error',
        headers: { 'X-Requested-With': 'XMLHttpRequest', 'X-CSRF-Token': token.obj },
      });
      if (!response.ok || (await response.json())?.success !== true) {
        throw new Error('Panel restart was not accepted');
      }
    } catch {
      messageApi.error(t('somethingWentWrong'));
      return;
    }
    messageApi.success(t('pages.index.restartPanelRequested'));
    await PromiseUtil.sleep(5000);
    window.location.reload();
  }, [messageApi, t]);

  const restartContainer = useCallback(async () => {
    let csrfToken = '';
    try {
      const token = await HttpUtil.get<string>('/csrf-token', undefined, { silent: true });
      if (token?.success && typeof token.obj === 'string') csrfToken = token.obj;
    } catch {
      // A missing CSRF token means no lifecycle request is sent.
    }
    if (!csrfToken) {
      messageApi.error(t('somethingWentWrong'));
      return;
    }
    let uncertain = false;
    try {
      const response = await fetch(withBasePath('/panel/api/server/restartRouterosContainer'), {
        method: 'POST',
        credentials: 'same-origin',
        redirect: 'error',
        headers: { 'X-Requested-With': 'XMLHttpRequest', 'X-CSRF-Token': csrfToken },
      });
      if (!response.ok) {
        messageApi.error(t('pages.index.restartContainerFailed'));
        return;
      }
      const body = await response.json();
      const result = ContainerRestartResultSchema.safeParse(body?.obj);
      if (body?.success !== true || !result.success) {
        messageApi.error(t('pages.index.restartContainerFailed'));
        return;
      }
      if (result.data.state === 'uncertain') uncertain = true;
      else if (result.data.state !== 'accepted') {
        messageApi.error(t('pages.index.restartContainerFailed'));
        return;
      }
    } catch {
      uncertain = true;
    }
    messageApi.info(
      t(
        uncertain
          ? 'pages.index.restartContainerUncertain'
          : 'pages.index.restartContainerRequested',
      ),
    );
    await PromiseUtil.sleep(10000);
    window.location.reload();
  }, [messageApi, t]);

  async function openConfig() {
    setLoading(true);
    try {
      const msg = await HttpUtil.get('/panel/api/server/getConfigJson');
      if (!msg?.success) return;
      setConfigText(JSON.stringify(msg.obj, null, 2));
      setConfigTextOpen(true);
    } finally {
      setLoading(false);
    }
  }

  async function copyConfig() {
    const ok = await ClipboardManager.copyText(configText || '');
    if (ok) messageApi.success(t('copied'));
  }

  function downloadConfig() {
    FileManager.downloadTextFile(configText, 'config.json');
  }

  const pageClass = `index-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();
  const totalDisk = status.disk.total;
  const freeDisk = Math.max(0, totalDisk - status.disk.current);

  const health = useMemo(() => {
    const items = [
      { name: t('pages.index.cpu'), value: status.cpu.percent },
      { name: t('pages.index.memory'), value: status.mem.percent },
      { name: t('pages.index.storage'), value: status.disk.percent },
    ];
    const list = (xs: typeof items) => xs.map((i) => `${i.name} ${i.value.toFixed(0)}%`).join(', ');
    const crit = items.filter((i) => i.value >= USAGE_CRIT_PERCENT);
    if (crit.length)
      return {
        text: t('pages.index.healthCritical', { list: list(crit) }),
        color: USAGE_CRIT_COLOR,
      };
    const warm = items.filter((i) => i.value >= USAGE_WARN_PERCENT);
    if (warm.length)
      return { text: t('pages.index.healthWarm', { list: list(warm) }), color: USAGE_WARN_COLOR };
    return null;
  }, [status, t]);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />

        <Layout className="content-shell">
          <Layout.Content className="content-area">
            <Spin
              spinning={loading || !fetched}
              delay={200}
              description={loading ? loadingTip : t('loading')}
              size="large"
            >
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={
                    <Button type="primary" onClick={refresh}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : (
                <div className="ov-page">
                  <OverviewActionBar
                    status={status}
                    isMobile={isMobile}
                    accessLogEnable={accessLogEnable}
                    panelVersion={displayVersion}
                    routerOSStatus={
                      routerOSAPIStatus.data ?? { state: 'connecting', checkedAt: 0, stale: false }
                    }
                    containerStatus={
                      routerOSContainerStatus.data ?? {
                        state: 'connecting',
                        status: '',
                        restartAvailable: false,
                      }
                    }
                    onStopXray={stopXray}
                    onRestartXray={restartXray}
                    onRestartPanel={restartPanel}
                    onRestartContainer={restartContainer}
                    onOpenLogs={() => setLogsOpen(true)}
                    onOpenXrayLogs={() => setXrayLogsOpen(true)}
                    onOpenAmneziaWGLogs={() => setAmneziawgLogsOpen(true)}
                    onOpenConfig={openConfig}
                    onOpenBackup={() => setBackupOpen(true)}
                    onOpenSystemHistory={() => setSysHistoryOpen(true)}
                    onOpenXrayMetrics={() => setXrayMetricsOpen(true)}
                  />

                  <SponsorSlot slot="dashboard" />

                  {health && (
                    <div className="ov-health" style={{ color: health.color }}>
                      <span className="ov-health-mark" />
                      {health.text}
                    </div>
                  )}

                  <hr className="ov-rule" />

                  <div className="ov-vitals">
                    {(() => {
                      const ros = routerOSAPIStatus.data;
                      const rosValid =
                        !routerOSAPIStatus.isError &&
                        ros?.state === 'connected' &&
                        ros.cpuLoad != null;
                      const rosLoad = rosValid ? (ros.cpuLoad as number) : 0;
                      const rosColor =
                        rosLoad >= USAGE_CRIT_PERCENT
                          ? USAGE_CRIT_COLOR
                          : rosLoad >= USAGE_WARN_PERCENT
                            ? USAGE_WARN_COLOR
                            : '#1677ff';
                      const rosUnavailableText = routerOSAPIStatus.isError
                        ? t('pages.index.unavailable')
                        : !ros || ros.state === 'connecting'
                          ? undefined
                          : ros.state === 'not_configured'
                            ? t('pages.index.routerosApiNotConfigured')
                            : t('pages.index.unavailable');
                      const rosDetail =
                        rosValid && ros.cpuCount != null && ros.cpuCount > 0
                          ? ros.cpuFrequencyMhz != null && ros.cpuFrequencyMhz > 0
                            ? `${t('pages.index.logicalCpus', { cpus: ros.cpuCount })} · ${CPUFormatter.cpuSpeedFormat(ros.cpuFrequencyMhz)}`
                            : t('pages.index.logicalCpus', { cpus: ros.cpuCount })
                          : '—';
                      const rosFootLeft =
                        rosValid && ros.cpuAvg != null
                          ? `${t('pages.index.avg')} ${ros.cpuAvg.toFixed(0)}%`
                          : '—';
                      const rosFootRight =
                        rosValid && ros.cpuPeak != null
                          ? `${t('pages.index.peak')} ${ros.cpuPeak}%`
                          : '—';
                      const rosSeries = rosValid ? (ros.cpuHistory ?? []) : [];
                      const containerCpuValue =
                        status.resourceScope === 'container' && status.metricAvailable('cpu')
                          ? `${status.cpu.percent.toFixed(1)}%`
                          : '—';

                      return (
                        <VitalTile
                          icon={<DashboardOutlined aria-hidden />}
                          label={t('pages.index.cpu')}
                          available={rosValid}
                          unavailableText={rosUnavailableText}
                          stale={rosValid && Boolean(ros.stale)}
                          staleLabel={t('pages.index.stale')}
                          secondaryLabel={t('pages.index.container')}
                          secondaryValue={containerCpuValue}
                          percent={rosLoad}
                          statusColor={rosColor}
                          detail={rosDetail}
                          footLeft={rosFootLeft}
                          footRight={rosFootRight}
                          data={rosSeries}
                          isMobile={isMobile}
                        />
                      );
                    })()}
                    {(() => {
                      const ros = routerOSAPIStatus.data;
                      const rosMemValid =
                        !routerOSAPIStatus.isError &&
                        ros?.state === 'connected' &&
                        ros.memPercent != null &&
                        ros.memUsed != null &&
                        ros.memTotal != null &&
                        ros.memTotal > 0;
                      const rosMemPct = rosMemValid ? (ros.memPercent as number) : 0;
                      const rosMemColor =
                        rosMemPct >= USAGE_CRIT_PERCENT
                          ? USAGE_CRIT_COLOR
                          : rosMemPct >= USAGE_WARN_PERCENT
                            ? USAGE_WARN_COLOR
                            : '#1677ff';
                      const rosMemUnavailableText = routerOSAPIStatus.isError
                        ? t('pages.index.unavailable')
                        : !ros || ros.state === 'connecting'
                          ? undefined
                          : ros.state === 'not_configured'
                            ? t('pages.index.routerosApiNotConfigured')
                            : t('pages.index.unavailable');
                      const rosMemDetail = rosMemValid
                        ? `${SizeFormatter.sizeFormat(ros.memUsed)} / ${SizeFormatter.sizeFormat(ros.memTotal)}`
                        : '—';
                      const rosMemFootLeft =
                        rosMemValid && ros.memAvg != null
                          ? `${t('pages.index.avg')} ${ros.memAvg.toFixed(0)}%`
                          : '—';
                      const rosMemFootRight =
                        rosMemValid && ros.memPeak != null
                          ? `${t('pages.index.peak')} ${ros.memPeak.toFixed(0)}%`
                          : '—';
                      const rosMemSeries = rosMemValid ? (ros.memHistory ?? []) : [];
                      const cm = status.containerMem;
                      const containerRamValue =
                        cm != null
                          ? cm.limit != null && cm.limit > 0
                            ? `${SizeFormatter.sizeFormat(cm.used)} / ${SizeFormatter.sizeFormat(cm.limit)}`
                            : `${SizeFormatter.sizeFormat(cm.used)} · ${t('pages.index.noLimit')}`
                          : '—';

                      return (
                        <VitalTile
                          icon={<DatabaseOutlined aria-hidden />}
                          label={t('pages.index.memory')}
                          available={rosMemValid}
                          unavailableText={rosMemUnavailableText}
                          stale={rosMemValid && Boolean(ros.stale)}
                          staleLabel={t('pages.index.stale')}
                          secondaryLabel={t('pages.index.container')}
                          secondaryValue={containerRamValue}
                          percent={rosMemPct}
                          statusColor={rosMemColor}
                          detail={rosMemDetail}
                          footLeft={rosMemFootLeft}
                          footRight={rosMemFootRight}
                          data={rosMemSeries}
                          isMobile={isMobile}
                        />
                      );
                    })()}
                    <ClientsSummaryTile
                      summary={clientsSummary.data}
                      isLoading={clientsSummary.isLoading}
                      isError={clientsSummary.isError}
                    />
                    <TopInboundsTile
                      inbounds={topInbounds.data}
                      isLoading={topInbounds.isLoading}
                      isError={topInbounds.isError}
                    />
                    <VitalTile
                      icon={<HddOutlined aria-hidden />}
                      label={t('pages.index.storage')}
                      percent={status.disk.percent}
                      statusColor={status.disk.color}
                      detail={`${SizeFormatter.sizeFormat(status.disk.current)} / ${SizeFormatter.sizeFormat(totalDisk)}`}
                      footLeft={`${t('pages.index.free')} ${SizeFormatter.sizeFormat(freeDisk)}`}
                      footRight={`${t('pages.index.avg')} ${mean(history.series.diskUsage).toFixed(1)}%`}
                      data={history.series.diskUsage}
                      isMobile={isMobile}
                    />
                    <SslCertTile tlsCert={status.tlsCert} />
                  </div>

                  <div className="ov-mid">
                    <ThroughputCard
                      status={status}
                      up={history.series.netUp}
                      down={history.series.netDown}
                      labels={history.labels}
                      isMobile={isMobile}
                    />
                    <ConnectionsCard
                      status={status}
                      tcp={history.series.tcpCount}
                      udp={history.series.udpCount}
                      labels={history.labels}
                      isMobile={isMobile}
                    />
                  </div>

                  <SystemStrip
                    status={status}
                    showIp={showIp}
                    onToggleIp={() => setShowIp((v) => !v)}
                  />
                </div>
              )}
            </Spin>
          </Layout.Content>
        </Layout>

        <LazyMount when={logsOpen}>
          <LogModal open={logsOpen} onClose={() => setLogsOpen(false)} />
        </LazyMount>
        <LazyMount when={backupOpen}>
          <BackupModal
            open={backupOpen}
            basePath={basePath}
            onClose={() => setBackupOpen(false)}
            onBusy={setBusy}
          />
        </LazyMount>
        <LazyMount when={sysHistoryOpen}>
          <SystemHistoryModal
            open={sysHistoryOpen}
            status={status}
            onClose={() => setSysHistoryOpen(false)}
          />
        </LazyMount>
        <LazyMount when={xrayMetricsOpen}>
          <XrayMetricsModal open={xrayMetricsOpen} onClose={() => setXrayMetricsOpen(false)} />
        </LazyMount>
        <LazyMount when={xrayLogsOpen}>
          <XrayLogModal open={xrayLogsOpen} onClose={() => setXrayLogsOpen(false)} />
        </LazyMount>
        <LazyMount when={amneziawgLogsOpen}>
          <AmneziaWGLogModal open={amneziawgLogsOpen} onClose={() => setAmneziawgLogsOpen(false)} />
        </LazyMount>
        <LazyMount when={configTextOpen}>
          <Modal
            open={configTextOpen}
            title={t('pages.index.xrayConfig')}
            width={isMobile ? '100%' : 900}
            style={isMobile ? { top: 20, maxWidth: 'calc(100vw - 16px)' } : { top: 20 }}
            onCancel={() => setConfigTextOpen(false)}
            footer={[
              <Button
                key="download"
                onClick={downloadConfig}
                size={isMobile ? 'small' : 'middle'}
                icon={<CloudDownloadOutlined aria-hidden />}
              >
                {isMobile ? 'Download' : 'config.json'}
              </Button>,
              <Button
                key="copy"
                type="primary"
                onClick={copyConfig}
                size={isMobile ? 'small' : 'middle'}
                icon={<CopyOutlined aria-hidden />}
              >
                Copy
              </Button>,
            ]}
          >
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 12 }}
              title={t('pages.index.xrayConfigSensitiveWarning')}
            />
            <JsonEditor
              value={configText}
              onChange={setConfigText}
              minHeight={isMobile ? '300px' : 'calc(100vh - 220px)'}
              maxHeight={isMobile ? '70vh' : 'calc(100vh - 220px)'}
              readOnly
            />
          </Modal>
        </LazyMount>
      </Layout>
    </ConfigProvider>
  );
}
