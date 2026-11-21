import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Form, Modal, Select, Space, Tabs } from 'antd';
import { DownloadOutlined, SyncOutlined } from '@ant-design/icons';

import { HttpUtil, FileManager, PromiseUtil } from '@/utils';
import { activateOnKey } from '@/utils/a11y';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useRouterOSAPIStatus } from '@/api/queries/useRouterOSAPIStatus';
import { useRouterOSContainerLogs } from '@/api/queries/useRouterOSContainerLogs';
import { parseLogLine } from './logParse';
import './LogModal.css';

interface LogModalProps {
  open: boolean;
  onClose: () => void;
}

const AUTO_UPDATE_INTERVAL = 5000;

export default function LogModal({ open, onClose }: LogModalProps) {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const [rows, setRows] = useState('20');
  const [level, setLevel] = useState('info');
  const [autoUpdate, setAutoUpdate] = useState(false);
  const [loading, setLoading] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const [tab, setTab] = useState('panel');
  const [xrayLogs, setXrayLogs] = useState<string[]>([]);
  const routerOSQuery = useRouterOSAPIStatus(open && tab === 'routeros');
  const routerOSLogsQuery = useRouterOSContainerLogs(
    Math.min(Number(rows), 100),
    open && tab === 'routeros',
    autoUpdate,
  );
  const refetchRouterOSLogs = routerOSLogsQuery.refetch;
  const routerOSState =
    routerOSQuery.data?.state ?? (routerOSQuery.isError ? 'unreachable' : 'connecting');
  const routerOSStale = routerOSQuery.data?.stale ?? false;
  const routerOSStateKeys: Record<string, string> = {
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

  const runRefresh = useCallback(async () => {
    if (!open || tab === 'routeros') {
      return;
    }
    try {
      const source = tab === 'xray' ? 'xrayRuntimeLogs' : 'logs';
      const msg = await HttpUtil.post<string[]>(`/panel/api/server/${source}/${rows}`, {
        level,
      });
      if (msg?.success) {
        if (tab === 'xray') setXrayLogs(msg.obj || []);
        else setLogs(msg.obj || []);
      }
      await PromiseUtil.sleep(300);
    } finally {
      setLoading(false);
    }
  }, [rows, level, tab, open]);

  const refresh = useCallback(() => {
    if (tab === 'routeros') {
      void refetchRouterOSLogs();
      return;
    }
    setLoading(true);
    void runRefresh();
  }, [runRefresh, refetchRouterOSLogs, tab]);

  const refreshRef = useRef(refresh);
  useEffect(() => {
    refreshRef.current = refresh;
  });

  // The spinner is raised during render so the fetch effect stays side-effect
  // free until its response lands.
  const refreshKey = open && tab !== 'routeros' ? `${rows}\u0000${level}\u0000${tab}` : null;
  const [loadingKey, setLoadingKey] = useState<string | null>(null);
  if (refreshKey !== loadingKey) {
    setLoadingKey(refreshKey);
    if (refreshKey) setLoading(true);
  }

  useEffect(() => {
    if (open && tab !== 'routeros') void runRefresh();
  }, [open, runRefresh, tab]);

  useEffect(() => {
    if (!open || !autoUpdate) return;
    const id = setInterval(() => refreshRef.current(), AUTO_UPDATE_INTERVAL);
    return () => clearInterval(id);
  }, [open, autoUpdate]);

  const containerEntries = routerOSLogsQuery.data?.entries ?? [];
  const shownLogs =
    tab === 'routeros'
      ? containerEntries.map((entry) => `${entry.time} ${entry.message}`)
      : tab === 'xray'
        ? xrayLogs
        : logs;
  const parsedLogs = useMemo(
    () => shownLogs.map((line) => parseLogLine(line, tab === 'xray' ? 'XRAY:' : 'X-UI:')),
    [shownLogs, tab],
  );

  function download() {
    FileManager.downloadTextFile(
      shownLogs.join('\n'),
      tab === 'routeros'
        ? 'routeros-container.log'
        : tab === 'xray'
          ? 'xray-runtime.log'
          : 'mikrozia-panel.log',
    );
  }

  const titleNode = (
    <>
      {t('pages.index.logs')}
      <SyncOutlined
        spin={loading || (tab === 'routeros' && routerOSLogsQuery.isFetching)}
        className="reload-icon"
        role="button"
        tabIndex={0}
        aria-label={t('refresh')}
        onClick={refresh}
        onKeyDown={activateOnKey(refresh)}
      />
    </>
  );

  return (
    <Modal
      open={open}
      footer={null}
      width={isMobile ? '100vw' : 800}
      style={isMobile ? { top: 0, paddingBottom: 0, maxWidth: '100vw' } : undefined}
      className={isMobile ? 'logmodal-mobile' : undefined}
      onCancel={onClose}
      title={titleNode}
    >
      <Tabs
        activeKey={tab}
        onChange={(key) => {
          setTab(key);
          if (key === 'routeros') setLoading(false);
        }}
        items={[
          { key: 'panel', label: t('pages.index.panel') },
          { key: 'xray', label: 'Xray' },
          { key: 'routeros', label: t('pages.index.routerosContainerTab') },
        ]}
      />
      {tab !== 'routeros' ? (
        <Form layout="inline" className="log-toolbar">
          <Form.Item>
            <Space.Compact>
              <Select
                aria-label={t('pages.index.logLines')}
                value={rows}
                size="small"
                style={{ width: 100 }}
                onChange={setRows}
                options={[
                  { value: '20', label: '20' },
                  { value: '50', label: '50' },
                  { value: '100', label: '100' },
                  { value: '500', label: '500' },
                  { value: '1000', label: '1000' },
                ]}
              />
              <Select
                aria-label={t('pages.xray.logLevel')}
                value={level}
                size="small"
                style={{ minWidth: 95 }}
                onChange={setLevel}
                options={[
                  { value: 'debug', label: t('pages.index.logLevelDebug') },
                  { value: 'info', label: t('pages.index.logLevelInfo') },
                  { value: 'notice', label: t('pages.index.logLevelNotice') },
                  { value: 'warning', label: t('pages.index.logLevelWarning') },
                  { value: 'error', label: t('pages.index.logLevelError') },
                ]}
              />
            </Space.Compact>
          </Form.Item>
          <Form.Item>
            <Checkbox checked={autoUpdate} onChange={(e) => setAutoUpdate(e.target.checked)}>
              {t('pages.index.autoUpdate')}
            </Checkbox>
          </Form.Item>
          <Form.Item className="download-item">
            <Button
              type="primary"
              onClick={download}
              icon={<DownloadOutlined aria-hidden />}
              aria-label={t('download')}
            />
          </Form.Item>
        </Form>
      ) : (
        <>
          <Form layout="inline" className="log-toolbar">
            <Form.Item>
              <Select
                aria-label={t('pages.index.logLines')}
                value={rows}
                size="small"
                style={{ width: 100 }}
                onChange={setRows}
                options={['20', '50', '100'].map((value) => ({ value, label: value }))}
              />
            </Form.Item>
            <Form.Item>
              <Checkbox checked={autoUpdate} onChange={(e) => setAutoUpdate(e.target.checked)}>
                {t('pages.index.autoUpdate')}
              </Checkbox>
            </Form.Item>
            <Form.Item className="download-item">
              <Button
                type="primary"
                onClick={download}
                disabled={containerEntries.length === 0}
                icon={<DownloadOutlined aria-hidden />}
                aria-label={t('download')}
              />
            </Form.Item>
          </Form>
          <Alert
            type={routerOSState === 'connected' && !routerOSStale ? 'success' : 'info'}
            showIcon
            style={{ marginBottom: 12 }}
            title={`${t('pages.index.routerosStatusPrefix')}: ${t(routerOSStale ? 'pages.index.routerosStale' : (routerOSStateKeys[routerOSState] ?? 'pages.index.routerosUnreachable'))}`}
          />
          {routerOSLogsQuery.data?.state !== 'connected' && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 12 }}
              title={t(
                routerOSLogsQuery.data?.state === 'logging_disabled'
                  ? 'pages.index.routerosContainerLoggingDisabled'
                  : routerOSLogsQuery.data?.state === 'identity_unverified'
                    ? 'pages.index.routerosContainerIdentityUnverified'
                    : 'pages.index.routerosLogsUnavailableGuidance',
              )}
            />
          )}
          <div className={`log-container ${isMobile ? 'log-container-mobile' : ''}`}>
            {containerEntries.length === 0 ? (
              <div className="log-empty">{t('pages.index.routerosContainerLogsEmpty')}</div>
            ) : (
              containerEntries.map((entry, index) => (
                <div className="log-line" key={`${entry.time}-${index}`}>
                  <span className="log-stamp">{entry.time}</span> - <span>{entry.message}</span>
                </div>
              ))
            )}
          </div>
        </>
      )}

      {tab === 'panel' && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          title={t('pages.index.panelLogsSource')}
          description={t('pages.index.panelLogsDesc')}
        />
      )}
      {tab === 'xray' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          title={t('pages.index.xrayRuntimeLogsSource')}
          description={t('pages.index.xrayRuntimeLogsDesc')}
        />
      )}

      {tab !== 'routeros' && (
        <div className={`log-container ${isMobile ? 'log-container-mobile' : ''}`}>
          {parsedLogs.length === 0 ? (
            <div className="log-empty">No Record...</div>
          ) : isMobile ? (
            parsedLogs.map((log, idx) => (
              <div key={idx} className="log-card">
                <div className="log-card-head">
                  {log.stamp && (
                    <span className="log-time">
                      {log.time && <span>{log.time}</span>}
                      {log.time && log.date ? ' ' : ''}
                      {log.date && <span className="log-date">{log.date}</span>}
                    </span>
                  )}
                  {log.levelText && (
                    <span className={`log-level-badge ${log.levelClass}`}>{log.levelText}</span>
                  )}
                </div>
                {(log.body || log.service) && (
                  <div className="log-body">
                    {log.service && <b>{log.service}</b>}
                    {log.service && log.body ? ' ' : ''}
                    {log.body && <span className="log-body-text">{log.body}</span>}
                  </div>
                )}
              </div>
            ))
          ) : (
            parsedLogs.map((log, idx) => (
              <div key={idx} className="log-line">
                {log.stamp && <span className="log-stamp">{log.stamp}</span>}
                {log.stamp && log.levelText ? ' ' : ''}
                {log.levelText && (
                  <span className={`log-level ${log.levelClass}`}>{log.levelText}</span>
                )}
                {(log.body || log.service) && (
                  <>
                    {(log.stamp || log.levelText) && <span> - </span>}
                    {log.service && <b>{log.service}</b>}
                    {log.service && log.body ? ' ' : ''}
                    <span>{log.body}</span>
                  </>
                )}
              </div>
            ))
          )}
        </div>
      )}
    </Modal>
  );
}
