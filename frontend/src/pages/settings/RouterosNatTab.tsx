import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Collapse, Input, InputNumber, Space, Switch, Typography } from 'antd';
import { ApiOutlined, BookOutlined, RadarChartOutlined, SyncOutlined } from '@ant-design/icons';
import { HttpUtil } from '@/utils';
import { onNumber } from '@/utils/onNumber';
import type { AllSetting } from '@/models/setting';
import { SettingListItem } from '@/components/ui';
import SecretInput from './SecretInput';

interface RouterosNatTabProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

interface ActionResult {
  success: boolean;
  msg: string;
}

interface TestNatObj {
  identity?: string;
  version?: string;
  natRules?: number;
}

interface SyncNatObj {
  synced?: number;
  failed?: number;
  errors?: string[];
}

function subnetHint(ip: string): string {
  const trimmed = ip.trim();
  const parts = trimmed.split('.');
  if (parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)) {
    return `${parts[0]}.${parts[1]}.${parts[2]}.0/24`;
  }
  return '172.16.10.0/24';
}

export default function RouterosNatTab({ allSetting, updateSetting }: RouterosNatTabProps) {
  const { t } = useTranslation();
  const [testLoading, setTestLoading] = useState(false);
  const [syncLoading, setSyncLoading] = useState(false);
  const [detectLoading, setDetectLoading] = useState(false);
  const [result, setResult] = useState<ActionResult | null>(null);

  const cliSnippets = useMemo(() => {
    const user = (allSetting.routerosNatUsername || 'mikrozia-nat').trim() || 'mikrozia-nat';
    const port = allSetting.routerosNatPort > 0 ? allSetting.routerosNatPort : 8728;
    const subnet = subnetHint(allSetting.routerosNatTargetIP || allSetting.routerosNatHost);
    const ifList = (allSetting.routerosNatInInterfaceList || 'WAN').trim() || 'WAN';
    return {
      userCmd: `/user/group/add name=mikrozia-nat policy=read,write,api\n/user/add name=${user} group=mikrozia-nat password="CHANGE_ME_STRONG_PASSWORD"`,
      apiCmd: `/ip/service/set api disabled=no port=${port} address=${subnet}`,
      firewallCmd: `/ip/firewall/filter/add chain=input action=accept protocol=tcp dst-port=${port} src-address=${subnet} comment="Allow Mikrozia container API"`,
      wanCmd: `/interface/list/member/print where list=${ifList}`,
      verifyCmd: `/ip/firewall/nat/print where comment~"^mikrozia:managed:inbound:"`,
    };
  }, [
    allSetting.routerosNatHost,
    allSetting.routerosNatInInterfaceList,
    allSetting.routerosNatPort,
    allSetting.routerosNatTargetIP,
    allSetting.routerosNatUsername,
  ]);

  async function handleDetectIp() {
    setDetectLoading(true);
    try {
      const res = (await HttpUtil.get('/panel/api/setting/detectContainerIp')) as {
        success?: boolean;
        obj?: { ip?: string };
      };
      const ip = res?.obj?.ip?.trim() ?? '';
      if (ip) {
        updateSetting({ routerosNatTargetIP: ip });
      } else {
        setResult({
          success: false,
          msg: t('pages.settings.routerosNatDetectFailed'),
        });
      }
    } catch (e: unknown) {
      setResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setDetectLoading(false);
    }
  }

  async function handleTestConnection() {
    setTestLoading(true);
    setResult(null);
    try {
      const res = (await HttpUtil.post(
        '/panel/api/setting/testRouterosNat',
        {
          host: allSetting.routerosNatHost,
          port: allSetting.routerosNatPort,
          username: allSetting.routerosNatUsername,
          password: allSetting.routerosNatPassword,
        },
        { silent: true },
      )) as { success?: boolean; msg?: string; obj?: TestNatObj };

      if (res?.success) {
        const info = res.obj;
        const details = [
          info?.identity ? `Router: ${info.identity}` : '',
          info?.version ? `RouterOS ${info.version}` : '',
          typeof info?.natRules === 'number' ? `NAT rules: ${info.natRules}` : '',
        ]
          .filter(Boolean)
          .join(' • ');
        setResult({
          success: true,
          msg: details
            ? `${t('pages.settings.routerosNatTestSuccess')} (${details})`
            : t('pages.settings.routerosNatTestSuccess'),
        });
      } else {
        setResult({
          success: false,
          msg: res?.msg || t('pages.settings.requestFailed'),
        });
      }
    } catch (e: unknown) {
      setResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setTestLoading(false);
    }
  }

  async function handleSyncAll() {
    setSyncLoading(true);
    setResult(null);
    try {
      const res = (await HttpUtil.post('/panel/api/setting/syncRouterosNat', undefined, {
        silent: true,
      })) as { success?: boolean; msg?: string; obj?: SyncNatObj };

      if (res?.success) {
        const synced = res.obj?.synced ?? 0;
        setResult({
          success: true,
          msg: `${t('pages.settings.routerosNatSyncSuccess')} (${synced})`,
        });
      } else {
        setResult({
          success: false,
          msg: res?.msg || t('pages.settings.routerosNatSyncPartial'),
        });
      }
    } catch (e: unknown) {
      setResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setSyncLoading(false);
    }
  }

  return (
    <>
      <Alert
        type="info"
        showIcon
        title={t('pages.settings.routerosNatNoticeTitle')}
        description={t('pages.settings.routerosNatNoticeDesc')}
        style={{ marginBottom: 16 }}
      />

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatEnable')}
        description={t('pages.settings.routerosNatEnableDesc')}
      >
        <Switch
          checked={allSetting.routerosNatEnable}
          onChange={(v) => updateSetting({ routerosNatEnable: v })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatHost')}
        description={t('pages.settings.routerosNatHostDesc')}
      >
        <Input
          value={allSetting.routerosNatHost}
          placeholder="172.16.10.1"
          onChange={(e) => updateSetting({ routerosNatHost: e.target.value })}
          style={{ width: '100%' }}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatPort')}
        description={t('pages.settings.routerosNatPortDesc')}
      >
        <InputNumber
          value={allSetting.routerosNatPort}
          min={1}
          max={65535}
          onChange={onNumber((v) => updateSetting({ routerosNatPort: v }))}
          style={{ width: '100%' }}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatUsername')}
        description={t('pages.settings.routerosNatUsernameDesc')}
      >
        <Input
          value={allSetting.routerosNatUsername}
          placeholder="mikrozia-nat"
          autoComplete="off"
          onChange={(e) => updateSetting({ routerosNatUsername: e.target.value })}
          style={{ width: '100%' }}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatPassword')}
        description={
          allSetting.hasRouterosNatPassword && !allSetting.clearRouterosNatPassword
            ? t('pages.settings.routerosNatPasswordConfigured')
            : t('pages.settings.routerosNatPasswordDesc')
        }
      >
        <SecretInput
          value={allSetting.routerosNatPassword}
          configured={allSetting.hasRouterosNatPassword}
          clearArmed={allSetting.clearRouterosNatPassword}
          placeholder={t('pages.settings.routerosNatPasswordPlaceholder')}
          onChange={(v) => updateSetting({ routerosNatPassword: v })}
          onClearArmedChange={(armed) => updateSetting({ clearRouterosNatPassword: armed })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatTargetIP')}
        description={t('pages.settings.routerosNatTargetIPDesc')}
      >
        <Space.Compact style={{ width: '100%' }}>
          <Input
            aria-label={t('pages.settings.routerosNatTargetIP')}
            value={allSetting.routerosNatTargetIP}
            placeholder="172.16.10.2"
            onChange={(e) => updateSetting({ routerosNatTargetIP: e.target.value })}
          />
          <Button
            icon={<RadarChartOutlined aria-hidden />}
            loading={detectLoading}
            onClick={handleDetectIp}
          >
            {t('pages.settings.routerosNatDetectIP')}
          </Button>
        </Space.Compact>
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.routerosNatInInterfaceList')}
        description={t('pages.settings.routerosNatInInterfaceListDesc')}
      >
        <Input
          value={allSetting.routerosNatInInterfaceList}
          placeholder="WAN"
          onChange={(e) => updateSetting({ routerosNatInInterfaceList: e.target.value })}
          style={{ width: '100%' }}
        />
      </SettingListItem>

      <Space orientation="vertical" size={12} style={{ width: '100%', marginTop: 16 }}>
        <Space wrap>
          <Button
            type="primary"
            icon={<ApiOutlined aria-hidden />}
            loading={testLoading}
            onClick={handleTestConnection}
          >
            {t('pages.settings.routerosNatTestConnection')}
          </Button>
          <Button
            icon={<SyncOutlined aria-hidden />}
            loading={syncLoading}
            disabled={!allSetting.routerosNatEnable}
            onClick={handleSyncAll}
          >
            {t('pages.settings.routerosNatSyncAll')}
          </Button>
        </Space>

        {result && (
          <Alert
            type={result.success ? 'success' : 'error'}
            title={result.msg}
            showIcon
            closable={{ onClose: () => setResult(null) }}
          />
        )}

        <Collapse
          style={{ marginTop: 8 }}
          items={[
            {
              key: 'setup-guide',
              label: (
                <Space size={8}>
                  <BookOutlined aria-hidden />
                  <span>{t('pages.settings.routerosNatGuideTitle')}</span>
                </Space>
              ),
              children: (
                <Space orientation="vertical" size={12} style={{ width: '100%' }}>
                  <Typography.Text strong>
                    {t('pages.settings.routerosNatGuideStep1Title')}
                  </Typography.Text>
                  <Typography.Text type="secondary">
                    {t('pages.settings.routerosNatGuideStep1Desc')}
                  </Typography.Text>
                  <Typography.Paragraph
                    copyable={{ text: cliSnippets.userCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {cliSnippets.userCmd}
                  </Typography.Paragraph>

                  <Typography.Text strong>
                    {t('pages.settings.routerosNatGuideStep2Title')}
                  </Typography.Text>
                  <Typography.Text type="secondary">
                    {t('pages.settings.routerosNatGuideStep2Desc')}
                  </Typography.Text>
                  <Typography.Paragraph
                    copyable={{ text: cliSnippets.apiCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {cliSnippets.apiCmd}
                  </Typography.Paragraph>
                  <Typography.Paragraph
                    copyable={{ text: cliSnippets.firewallCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {cliSnippets.firewallCmd}
                  </Typography.Paragraph>

                  <Typography.Text strong>
                    {t('pages.settings.routerosNatGuideStep3Title')}
                  </Typography.Text>
                  <Typography.Text type="secondary">
                    {t('pages.settings.routerosNatGuideStep3Desc')}
                  </Typography.Text>
                  <Typography.Paragraph
                    copyable={{ text: cliSnippets.wanCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {cliSnippets.wanCmd}
                  </Typography.Paragraph>

                  <Typography.Text strong>
                    {t('pages.settings.routerosNatGuideStep4Title')}
                  </Typography.Text>
                  <Typography.Text type="secondary">
                    {t('pages.settings.routerosNatGuideStep4Desc')}
                  </Typography.Text>
                  <Typography.Paragraph
                    copyable={{ text: cliSnippets.verifyCmd }}
                    code
                    style={{
                      whiteSpace: 'pre-wrap',
                      direction: 'ltr',
                      textAlign: 'left',
                      margin: 0,
                    }}
                  >
                    {cliSnippets.verifyCmd}
                  </Typography.Paragraph>
                </Space>
              ),
            },
          ]}
        />
      </Space>
    </>
  );
}
