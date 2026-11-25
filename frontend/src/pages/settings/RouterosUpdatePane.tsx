import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  Descriptions,
  Modal,
  Space,
  Tag,
  Typography,
} from 'antd';
import {
  CloudDownloadOutlined,
  DeleteOutlined,
  ReloadOutlined,
  RocketOutlined,
} from '@ant-design/icons';
import { HttpUtil, SizeFormatter } from '@/utils';
import { useMediaQuery } from '@/hooks/useMediaQuery';

interface ContainerUpdateSpec {
  id: string;
  name: string;
  tag: string;
  remoteImage: string;
  interface: string;
  rootDir: string;
  mountlists: string;
  envlist: string;
  dns: string;
  memoryHigh: string;
  memoryMax: string;
  status: string;
  comment: string;
}

interface RouterOSUpdateInfo {
  currentVersion: string;
  latestVersion: string;
  latestUpdatedAt?: string;
  architectures?: string[];
  updateAvailable: boolean;
  dockerHubError?: string;
  routerosState: string;
  freeHddSpace: number;
  totalHddSpace: number;
  currentContainer?: ContainerUpdateSpec;
  stagedContainer?: ContainerUpdateSpec;
  previous?: ContainerUpdateSpec[];
  manualStageCmd?: string;
  manualCutoverCmd?: string;
  manualCleanupCmd?: string;
}

export default function RouterosUpdatePane() {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const [modal, modalContextHolder] = Modal.useModal();

  const [info, setInfo] = useState<RouterOSUpdateInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [stageLoading, setStageLoading] = useState(false);
  const [cutoverLoading, setCutoverLoading] = useState(false);
  const [cleanupLoading, setCleanupLoading] = useState(false);
  const [cleanupAfterBoot, setCleanupAfterBoot] = useState(true);
  const [actionAlert, setActionAlert] = useState<{
    type: 'success' | 'warning' | 'error' | 'info';
    msg: string;
  } | null>(null);

  const loadInfo = useCallback(
    async (refresh = false) => {
      setLoading(true);
      try {
        const res = (await HttpUtil.get(
          `/panel/api/setting/routerosUpdateInfo${refresh ? '?refresh=1' : ''}`,
          undefined,
          { silent: true },
        )) as { success?: boolean; obj?: RouterOSUpdateInfo; msg?: string };
        if (res?.success && res.obj) {
          setInfo(res.obj);
        } else if (res?.msg) {
          setActionAlert({ type: 'error', msg: res.msg });
        }
      } catch (e: unknown) {
        setActionAlert({
          type: 'error',
          msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
        });
      } finally {
        setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const res = (await HttpUtil.get('/panel/api/setting/routerosUpdateInfo', undefined, {
          silent: true,
        })) as { success?: boolean; obj?: RouterOSUpdateInfo };
        if (active && res?.success && res.obj) {
          setInfo(res.obj);
        }
      } catch {
        // Initial load error can be retried via Check Docker Hub button
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  // Poll every 3s while a staged container is extracting on RouterOS
  const isExtracting = info?.stagedContainer?.status?.toLowerCase() === 'extracting';
  useEffect(() => {
    if (!isExtracting) return undefined;
    const timer = window.setInterval(() => {
      void loadInfo(false);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [isExtracting, loadInfo]);

  function handleStage() {
    if (!info?.latestVersion) return;
    modal.confirm({
      title: t('pages.settings.routerosUpdateStageConfirmTitle'),
      content: t('pages.settings.routerosUpdateStageConfirmDesc', {
        version: info.latestVersion,
      }),
      okText: t('confirm'),
      cancelText: t('cancel'),
      onOk: async () => {
        setStageLoading(true);
        setActionAlert(null);
        try {
          const res = (await HttpUtil.post(
            '/panel/api/setting/routerosUpdateStage',
            { targetVersion: info.latestVersion },
            { silent: true },
          )) as { success?: boolean; msg?: string };
          if (res?.success) {
            setActionAlert({
              type: 'info',
              msg: t('pages.settings.routerosUpdateStageStarted'),
            });
            await loadInfo(false);
          } else {
            setActionAlert({
              type: 'error',
              msg: res?.msg || t('pages.settings.requestFailed'),
            });
          }
        } catch (e: unknown) {
          setActionAlert({
            type: 'error',
            msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
          });
        } finally {
          setStageLoading(false);
        }
      },
    });
  }

  function handleCutover() {
    modal.confirm({
      title: t('pages.settings.routerosUpdateCutoverConfirmTitle'),
      content: t('pages.settings.routerosUpdateCutoverConfirmDesc'),
      okText: t('confirm'),
      cancelText: t('cancel'),
      onOk: async () => {
        setCutoverLoading(true);
        setActionAlert(null);
        try {
          const res = (await HttpUtil.post(
            '/panel/api/setting/routerosUpdateCutover',
            { cleanupPrevious: cleanupAfterBoot },
            { silent: true },
          )) as { success?: boolean; msg?: string; obj?: { state?: string } };
          if (res?.success) {
            setActionAlert({
              type: 'success',
              msg: t('pages.settings.routerosUpdateCutoverScheduled'),
            });
            window.setTimeout(() => {
              window.location.reload();
            }, 10000);
          } else if (res?.obj?.state === 'permission_required') {
            setActionAlert({
              type: 'warning',
              msg: t('pages.settings.routerosUpdatePolicyRequired'),
            });
          } else {
            setActionAlert({
              type: 'error',
              msg: res?.msg || t('pages.settings.requestFailed'),
            });
          }
        } catch (e: unknown) {
          setActionAlert({
            type: 'error',
            msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
          });
        } finally {
          setCutoverLoading(false);
        }
      },
    });
  }

  async function handleCleanupPrevious() {
    setCleanupLoading(true);
    setActionAlert(null);
    try {
      const res = (await HttpUtil.post(
        '/panel/api/setting/routerosUpdateCleanupPrevious',
        undefined,
        {
          silent: true,
        },
      )) as { success?: boolean; msg?: string; obj?: { removed?: number } };
      if (res?.success) {
        setActionAlert({
          type: 'success',
          msg: `${t('pages.settings.routerosUpdateCleanupSuccess')} (${res.obj?.removed ?? 0})`,
        });
        await loadInfo(false);
      } else {
        setActionAlert({
          type: 'error',
          msg: res?.msg || t('pages.settings.requestFailed'),
        });
      }
    } catch (e: unknown) {
      setActionAlert({
        type: 'error',
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setCleanupLoading(false);
    }
  }

  const canStage =
    Boolean(info?.latestVersion) &&
    info?.routerosState === 'ok' &&
    Boolean(info?.currentContainer) &&
    !info?.stagedContainer;

  const canCutover =
    info?.routerosState === 'ok' &&
    Boolean(info?.currentContainer) &&
    Boolean(info?.stagedContainer) &&
    !isExtracting;

  return (
    <Space orientation="vertical" size={12} style={{ width: '100%' }}>
      {modalContextHolder}
      <Alert
        type="info"
        showIcon
        title={t('pages.settings.routerosUpdateTitle')}
        description={t('pages.settings.routerosUpdateDesc')}
      />

      {actionAlert && (
        <Alert
          type={actionAlert.type}
          showIcon
          title={actionAlert.msg}
          closable={{ onClose: () => setActionAlert(null) }}
        />
      )}

      {info?.dockerHubError && <Alert type="warning" showIcon title={info.dockerHubError} />}

      <Card
        size="small"
        title={t('pages.settings.routerosUpdateDockerHubCard')}
        extra={
          <Button
            size="small"
            icon={<ReloadOutlined aria-hidden />}
            loading={loading}
            onClick={() => void loadInfo(true)}
          >
            {t('pages.settings.routerosUpdateCheckBtn')}
          </Button>
        }
      >
        <Space orientation="vertical" size={12} style={{ width: '100%' }}>
          <Descriptions
            bordered
            size="small"
            column={isMobile ? 1 : 2}
            items={[
              {
                key: 'current',
                label: t('pages.settings.routerosUpdateCurrent'),
                children: <Tag>{info?.currentVersion || '—'}</Tag>,
              },
              {
                key: 'latest',
                label: t('pages.settings.routerosUpdateLatest'),
                children: info?.latestVersion ? (
                  <Space size={6} wrap>
                    <Tag color={info.updateAvailable ? 'processing' : 'success'}>
                      {info.latestVersion}
                    </Tag>
                    {info.updateAvailable && (
                      <Tag color="gold">{t('pages.settings.routerosUpdateAvailableBadge')}</Tag>
                    )}
                  </Space>
                ) : (
                  '—'
                ),
              },
              {
                key: 'archs',
                label: t('pages.settings.routerosUpdateArchs'),
                children: info?.architectures?.length ? info.architectures.join(', ') : '—',
              },
              {
                key: 'disk',
                label: t('pages.settings.routerosHealthFreeDisk'),
                children:
                  info && info.freeHddSpace > 0 && info.totalHddSpace > 0
                    ? `${SizeFormatter.sizeFormat(info.freeHddSpace)} / ${SizeFormatter.sizeFormat(info.totalHddSpace)}`
                    : '—',
              },
            ]}
          />

          <Checkbox
            checked={cleanupAfterBoot}
            onChange={(e) => setCleanupAfterBoot(e.target.checked)}
          >
            {t('pages.settings.routerosUpdateCleanupAfterBoot')}
          </Checkbox>

          <Space wrap>
            <Button
              type="primary"
              icon={<CloudDownloadOutlined aria-hidden />}
              loading={stageLoading}
              disabled={!canStage}
              onClick={handleStage}
            >
              {t('pages.settings.routerosUpdateStageBtn')}
            </Button>
            {info?.stagedContainer && (
              <Button
                type="primary"
                icon={<RocketOutlined aria-hidden />}
                loading={cutoverLoading || isExtracting}
                disabled={!canCutover}
                onClick={handleCutover}
              >
                {isExtracting
                  ? t('pages.settings.routerosUpdateExtracting')
                  : t('pages.settings.routerosUpdateCutoverBtn')}
              </Button>
            )}
          </Space>
        </Space>
      </Card>

      {info?.stagedContainer && (
        <Card size="small" title={t('pages.settings.routerosUpdateStagedCard')}>
          <Descriptions
            bordered
            size="small"
            column={isMobile ? 1 : 2}
            items={[
              {
                key: 'image',
                label: 'Image',
                children: info.stagedContainer.remoteImage || info.stagedContainer.tag || '—',
              },
              {
                key: 'status',
                label: t('status'),
                children: (
                  <Tag color={isExtracting ? 'processing' : 'success'}>
                    {info.stagedContainer.status || '—'}
                  </Tag>
                ),
              },
              {
                key: 'rootDir',
                label: 'Root Dir',
                children: info.stagedContainer.rootDir || '—',
              },
              {
                key: 'mounts',
                label: 'Mounts',
                children: info.stagedContainer.mountlists || '—',
              },
            ]}
          />
        </Card>
      )}

      {info?.previous && info.previous.length > 0 && (
        <Card size="small" title={t('pages.settings.routerosUpdatePrevCard')}>
          <Space orientation="vertical" size={8} style={{ width: '100%' }}>
            <Typography.Text type="secondary">
              {t('pages.settings.routerosUpdatePrevDesc', { count: info.previous.length })}
            </Typography.Text>
            <Button
              danger
              icon={<DeleteOutlined aria-hidden />}
              loading={cleanupLoading}
              onClick={handleCleanupPrevious}
            >
              {t('pages.settings.routerosUpdateCleanupBtn')}
            </Button>
          </Space>
        </Card>
      )}

      {info?.manualStageCmd && (
        <Collapse
          size="small"
          items={[
            {
              key: 'manual',
              label: t('pages.settings.routerosUpdateManualTitle'),
              children: (
                <Space orientation="vertical" size={8} style={{ width: '100%' }}>
                  <Typography.Text strong>
                    1. {t('pages.settings.routerosUpdateManualStep1')}
                  </Typography.Text>
                  <Typography.Paragraph
                    copyable={{ text: info.manualStageCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {info.manualStageCmd}
                  </Typography.Paragraph>
                  {info.manualCutoverCmd && (
                    <>
                      <Typography.Text strong>
                        2. {t('pages.settings.routerosUpdateManualStep2')}
                      </Typography.Text>
                      <Typography.Paragraph
                        copyable={{ text: info.manualCutoverCmd }}
                        code
                        style={{
                          whiteSpace: 'pre-wrap',
                          direction: 'ltr',
                          textAlign: 'left',
                          margin: 0,
                        }}
                      >
                        {info.manualCutoverCmd}
                      </Typography.Paragraph>
                    </>
                  )}
                  {info.manualCleanupCmd && (
                    <>
                      <Typography.Text strong>
                        3. {t('pages.settings.routerosUpdateManualStep3')}
                      </Typography.Text>
                      <Typography.Paragraph
                        copyable={{ text: info.manualCleanupCmd }}
                        code
                        style={{
                          whiteSpace: 'pre-wrap',
                          direction: 'ltr',
                          textAlign: 'left',
                          margin: 0,
                        }}
                      >
                        {info.manualCleanupCmd}
                      </Typography.Paragraph>
                    </>
                  )}
                </Space>
              ),
            },
          ]}
        />
      )}
    </Space>
  );
}
