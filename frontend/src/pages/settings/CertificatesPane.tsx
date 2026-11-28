import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Checkbox, Input, Radio, Space, Tag, Typography, message } from 'antd';
import { ReloadOutlined, SafetyCertificateOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import type { AllSetting } from '@/models/setting';

interface ACMEDomainStatus {
  domain: string;
  mode: 'http-01' | 'dns-01' | string;
  expiry: string;
  installed: boolean;
  autoRenew: boolean;
  tokenConfigured: boolean;
  certPath: string;
  keyPath: string;
}

interface ACMEStatusResponse {
  toolInstalled: boolean;
  routerosReady: boolean;
  managedNatEnabled: boolean;
  webCertFile: string;
  webKeyFile: string;
  subCertFile: string;
  subKeyFile: string;
  domains: ACMEDomainStatus[];
}

interface ACMEIssueResponse {
  domain: string;
  mode: string;
  target?: 'both' | 'panel' | 'sub' | string;
  certPath: string;
  keyPath: string;
  notAfter: number;
  port80AutoManaged: boolean;
  port80Closed: boolean;
  warning?: string;
}

interface CertificatesPaneProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

export default function CertificatesPane({ allSetting, updateSetting }: CertificatesPaneProps) {
  const { t, i18n } = useTranslation();
  const [status, setStatus] = useState<ACMEStatusResponse | null>(null);
  const [loadingStatus, setLoadingStatus] = useState(false);

  const [mode, setMode] = useState<'http-01' | 'dns-01'>('http-01');
  const [target, setTarget] = useState<'both' | 'panel' | 'sub'>('both');
  const [domain, setDomain] = useState(() => allSetting.webDomain || '');
  const [email, setEmail] = useState('');
  const [cfToken, setCfToken] = useState('');
  const [autoOpenPort80, setAutoOpenPort80] = useState(true);

  const [issuing, setIssuing] = useState(false);
  const [renewingDomain, setRenewingDomain] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<{
    type: 'success' | 'warning' | 'error';
    title: string;
    description?: string;
  } | null>(null);

  const loadStatus = useCallback(async () => {
    setLoadingStatus(true);
    try {
      const msg = await HttpUtil.get<ACMEStatusResponse>(
        '/panel/api/setting/acmeStatus',
        undefined,
        {
          silent: true,
        },
      );
      if (msg?.success && msg.obj) {
        setStatus(msg.obj);
      }
    } finally {
      setLoadingStatus(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    HttpUtil.get<ACMEStatusResponse>('/panel/api/setting/acmeStatus', undefined, {
      silent: true,
    }).then((msg) => {
      if (!cancelled && msg?.success && msg.obj) {
        setStatus(msg.obj);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const formatExpiry = useCallback(
    (iso: string) => {
      if (!iso) return '—';
      const d = new Date(iso);
      if (Number.isNaN(d.getTime()) || d.getUTCFullYear() <= 1970) return '—';
      try {
        return new Intl.DateTimeFormat(i18n.language || undefined, {
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
        }).format(d);
      } catch {
        return iso.slice(0, 10);
      }
    },
    [i18n.language],
  );

  const applyPathsForTarget = (certPath: string, keyPath: string, appliedTarget?: string) => {
    const patch: Partial<AllSetting> = {};
    if (!appliedTarget || appliedTarget === 'both' || appliedTarget === 'panel') {
      patch.webCertFile = certPath;
      patch.webKeyFile = keyPath;
    }
    if (!appliedTarget || appliedTarget === 'both' || appliedTarget === 'sub') {
      patch.subCertFile = certPath;
      patch.subKeyFile = keyPath;
    }
    updateSetting(patch);
  };

  const handleIssue = async () => {
    const trimmedDomain = domain.trim();
    if (!trimmedDomain) {
      message.warning(t('pages.settings.acmeDomainRequired'));
      return;
    }
    if (mode === 'dns-01' && !cfToken.trim()) {
      message.warning(t('pages.settings.acmeCfTokenRequired'));
      return;
    }

    setIssuing(true);
    setFeedback(null);
    try {
      const msg = await HttpUtil.post<ACMEIssueResponse>(
        '/panel/api/setting/acmeIssue',
        {
          domain: trimmedDomain,
          mode,
          target,
          email: email.trim(),
          cloudflareToken: mode === 'dns-01' ? cfToken.trim() : '',
          autoOpenPort80: mode === 'http-01' && Boolean(status?.routerosReady) && autoOpenPort80,
        },
        { silent: true },
      );
      if (msg?.success && msg.obj) {
        const res = msg.obj;
        applyPathsForTarget(res.certPath, res.keyPath, res.target || target);
        setCfToken('');
        const notes = [res.certPath, res.keyPath];
        if (res.port80AutoManaged && res.port80Closed) {
          notes.push(t('pages.settings.acmePort80ClosedNote'));
        }
        if (res.warning) {
          notes.push(res.warning);
        }
        setFeedback({
          type: res.warning ? 'warning' : 'success',
          title: t('pages.settings.acmeIssueSuccess'),
          description: notes.join(' · '),
        });
        await loadStatus();
      } else {
        setFeedback({
          type: 'error',
          title: msg?.msg || t('somethingWentWrong'),
        });
      }
    } finally {
      setIssuing(false);
    }
  };

  const handleRenew = async (targetDomain: string, targetMode: string) => {
    setRenewingDomain(targetDomain);
    setFeedback(null);
    try {
      const msg = await HttpUtil.post<ACMEIssueResponse>(
        '/panel/api/setting/acmeRenew',
        {
          domain: targetDomain,
          autoOpenPort80:
            targetMode === 'http-01' && Boolean(status?.routerosReady) && autoOpenPort80,
        },
        { silent: true },
      );
      if (msg?.success && msg.obj) {
        applyPathsForTarget(msg.obj.certPath, msg.obj.keyPath, msg.obj.target);
        setFeedback({
          type: msg.obj.warning ? 'warning' : 'success',
          title: t('pages.settings.acmeRenewSuccess'),
          description: msg.obj.warning || msg.obj.certPath,
        });
        await loadStatus();
      } else {
        setFeedback({
          type: 'error',
          title: msg?.msg || t('somethingWentWrong'),
        });
      }
    } finally {
      setRenewingDomain(null);
    }
  };

  const routerosReady = Boolean(status?.routerosReady);

  return (
    <Space orientation="vertical" size={16} style={{ width: '100%', marginBottom: 16 }}>
      <Card
        size="small"
        title={
          <Space size={8}>
            <SafetyCertificateOutlined aria-hidden />
            <span>{t('pages.settings.acmeTitle')}</span>
          </Space>
        }
        extra={
          <Button
            size="small"
            icon={<ReloadOutlined aria-hidden />}
            loading={loadingStatus}
            onClick={() => void loadStatus()}
          />
        }
      >
        <Space orientation="vertical" size={14} style={{ width: '100%' }}>
          <Typography.Text type="secondary">{t('pages.settings.acmeDesc')}</Typography.Text>

          {status && !status.toolInstalled && (
            <Alert type="info" showIcon title={t('pages.settings.acmeToolMissing')} />
          )}

          <div>
            <Typography.Text strong style={{ display: 'block', marginBottom: 6 }}>
              {t('pages.settings.acmeMethod')}
            </Typography.Text>
            <Radio.Group
              optionType="button"
              buttonStyle="solid"
              value={mode}
              onChange={(e) => setMode(e.target.value)}
              options={[
                { label: t('pages.settings.acmeMethodHttp01'), value: 'http-01' },
                { label: t('pages.settings.acmeMethodDns01'), value: 'dns-01' },
              ]}
            />
          </div>

          <div>
            <Typography.Text strong style={{ display: 'block', marginBottom: 6 }}>
              {t('pages.settings.acmeTarget')}
            </Typography.Text>
            <Radio.Group
              optionType="button"
              buttonStyle="solid"
              value={target}
              disabled={issuing}
              onChange={(e) => setTarget(e.target.value)}
              options={[
                { label: t('pages.settings.acmeTargetBoth'), value: 'both' },
                { label: t('pages.settings.acmeTargetPanel'), value: 'panel' },
                { label: t('pages.settings.acmeTargetSub'), value: 'sub' },
              ]}
            />
            <div style={{ marginTop: 4 }}>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('pages.settings.acmeTargetHint')}
              </Typography.Text>
            </div>
          </div>

          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
              gap: 12,
            }}
          >
            <div>
              <Typography.Text strong style={{ display: 'block', marginBottom: 4 }}>
                {t('pages.settings.acmeDomain')}
              </Typography.Text>
              <Input
                aria-label={t('pages.settings.acmeDomain')}
                value={domain}
                onChange={(e) => setDomain(e.target.value)}
                placeholder={t('pages.settings.acmeDomainPlaceholder')}
                disabled={issuing}
              />
            </div>
            <div>
              <Typography.Text strong style={{ display: 'block', marginBottom: 4 }}>
                {t('pages.settings.acmeEmail')}
              </Typography.Text>
              <Input
                aria-label={t('pages.settings.acmeEmail')}
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={t('pages.settings.acmeEmailPlaceholder')}
                disabled={issuing}
              />
            </div>
          </div>

          {mode === 'http-01' ? (
            <div>
              <Checkbox
                checked={routerosReady && autoOpenPort80}
                disabled={!routerosReady || issuing}
                onChange={(e) => setAutoOpenPort80(e.target.checked)}
              >
                {t('pages.settings.acmeAutoPort80')}
              </Checkbox>
              {!routerosReady && (
                <div>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {t('pages.settings.acmeAutoPort80NotReady')}
                  </Typography.Text>
                </div>
              )}
            </div>
          ) : (
            <div>
              <Typography.Text strong style={{ display: 'block', marginBottom: 4 }}>
                {t('pages.settings.acmeCfToken')}
              </Typography.Text>
              <Input.Password
                aria-label={t('pages.settings.acmeCfToken')}
                value={cfToken}
                onChange={(e) => setCfToken(e.target.value)}
                placeholder={t('pages.settings.acmeCfTokenPlaceholder')}
                disabled={issuing}
                autoComplete="new-password"
              />
            </div>
          )}

          <div>
            <Button
              type="primary"
              icon={<SafetyCertificateOutlined aria-hidden />}
              loading={issuing}
              onClick={() => void handleIssue()}
            >
              {issuing ? t('pages.settings.acmeIssuing') : t('pages.settings.acmeIssueBtn')}
            </Button>
          </div>

          {feedback && (
            <Alert
              type={feedback.type}
              showIcon
              title={feedback.title}
              description={feedback.description}
            />
          )}

          {status && status.domains.length > 0 && (
            <div style={{ borderTop: '1px solid var(--ant-color-border)', paddingTop: 12 }}>
              <Typography.Text strong style={{ display: 'block', marginBottom: 8 }}>
                {t('pages.settings.acmeIssuedDomains')}
              </Typography.Text>
              <Space orientation="vertical" size={8} style={{ width: '100%' }}>
                {status.domains.map((d) => {
                  const onPanel =
                    Boolean(d.certPath) &&
                    (status.webCertFile === d.certPath || allSetting.webCertFile === d.certPath);
                  const onSub =
                    Boolean(d.certPath) &&
                    (status.subCertFile === d.certPath || allSetting.subCertFile === d.certPath);
                  const boundLabel =
                    onPanel && onSub
                      ? t('pages.settings.acmeBoundBoth')
                      : onPanel
                        ? t('pages.settings.acmeBoundPanel')
                        : onSub
                          ? t('pages.settings.acmeBoundSub')
                          : '';
                  return (
                    <div
                      key={d.domain}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        flexWrap: 'wrap',
                        gap: 8,
                        padding: '8px 10px',
                        borderRadius: 8,
                        border: '1px solid var(--ant-color-border)',
                      }}
                    >
                      <Space size={8} wrap>
                        <Typography.Text strong>{d.domain}</Typography.Text>
                        {boundLabel && <Tag color="purple">{boundLabel}</Tag>}
                        <Tag color={d.mode === 'dns-01' ? 'blue' : 'default'}>
                          {d.mode === 'dns-01' ? 'DNS-01' : 'HTTP-01'}
                        </Tag>
                        <Tag color={d.autoRenew ? 'success' : 'default'}>
                          {d.autoRenew
                            ? t('pages.settings.acmeAutoRenewTag')
                            : t('pages.settings.acmeManualRenewTag')}
                        </Tag>
                        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                          {formatExpiry(d.expiry)}
                        </Typography.Text>
                      </Space>
                      <Button
                        size="small"
                        loading={renewingDomain === d.domain}
                        onClick={() => void handleRenew(d.domain, d.mode)}
                      >
                        {t('pages.settings.acmeRenewBtn')}
                      </Button>
                    </div>
                  );
                })}
              </Space>
            </div>
          )}
        </Space>
      </Card>
    </Space>
  );
}
