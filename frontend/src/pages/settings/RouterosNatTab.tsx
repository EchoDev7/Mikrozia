import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Descriptions,
  Input,
  InputNumber,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { TableColumnsType } from 'antd';
import {
  ApiOutlined,
  BookOutlined,
  DeleteOutlined,
  RadarChartOutlined,
  ReloadOutlined,
  SettingOutlined,
  SyncOutlined,
  TableOutlined,
} from '@ant-design/icons';
import { HttpUtil, SizeFormatter } from '@/utils';
import { onNumber } from '@/utils/onNumber';
import type { AllSetting } from '@/models/setting';
import { SettingListItem } from '@/components/ui';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { catTabLabel } from './catTabLabel';
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
  boardName?: string;
  freeHddSpace?: number;
  totalHddSpace?: number;
  hasMasquerade?: boolean;
  natRules?: number;
  managedRules?: number;
}

interface SyncNatObj {
  synced?: number;
  failed?: number;
  errors?: string[];
}

interface ManagedNatRule {
  id: string;
  inboundId: number;
  protocol: string;
  dstPort: string;
  toAddress: string;
  toPort: string;
  inInterface?: string;
  dstAddressType?: string;
  disabled: boolean;
  bytes: number;
  packets: number;
  comment: string;
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
  const { isMobile } = useMediaQuery();
  const [testLoading, setTestLoading] = useState(false);
  const [syncLoading, setSyncLoading] = useState(false);
  const [detectLoading, setDetectLoading] = useState(false);
  const [detectWanLoading, setDetectWanLoading] = useState(false);
  const [result, setResult] = useState<ActionResult | null>(null);
  const [health, setHealth] = useState<TestNatObj | null>(null);

  const [rules, setRules] = useState<ManagedNatRule[]>([]);
  const [rulesLoading, setRulesLoading] = useState(false);
  const [cleanupLoading, setCleanupLoading] = useState(false);
  const [rulesResult, setRulesResult] = useState<ActionResult | null>(null);

  const cliSnippets = useMemo(() => {
    const user = (allSetting.routerosNatUsername || 'mikrozia-nat').trim() || 'mikrozia-nat';
    const port = allSetting.routerosNatPort > 0 ? allSetting.routerosNatPort : 8728;
    const subnet = subnetHint(allSetting.routerosNatTargetIP || allSetting.routerosNatHost);
    return {
      userCmd: `/user/group/add name=mikrozia-nat policy=read,write,api\n/user/add name=${user} group=mikrozia-nat password="CHANGE_ME_STRONG_PASSWORD"`,
      apiCmd: `/ip/service/set api disabled=no port=${port} address=${subnet}`,
      firewallCmd: `/ip/firewall/filter/add chain=input action=accept protocol=tcp dst-port=${port} src-address=${subnet} comment="Allow Mikrozia container API"`,
      verifyCmd: `/ip/firewall/nat/print where comment~"^mikrozia:managed:inbound:"`,
    };
  }, [
    allSetting.routerosNatHost,
    allSetting.routerosNatPort,
    allSetting.routerosNatTargetIP,
    allSetting.routerosNatUsername,
  ]);

  const loadRules = useCallback(async () => {
    setRulesLoading(true);
    setRulesResult(null);
    try {
      const res = (await HttpUtil.get('/panel/api/setting/routerosNatRules', undefined, {
        silent: true,
      })) as { success?: boolean; msg?: string; obj?: ManagedNatRule[] };
      if (res?.success && Array.isArray(res.obj)) {
        setRules(res.obj);
      } else {
        setRules([]);
        setRulesResult({
          success: false,
          msg: res?.msg || t('pages.settings.requestFailed'),
        });
      }
    } catch (e: unknown) {
      setRules([]);
      setRulesResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setRulesLoading(false);
    }
  }, [t]);

  async function handleCleanupOrphans() {
    setCleanupLoading(true);
    setRulesResult(null);
    try {
      const res = (await HttpUtil.post('/panel/api/setting/cleanupRouterosNat', undefined, {
        silent: true,
      })) as { success?: boolean; msg?: string; obj?: { removed?: number } };
      if (res?.success) {
        const removed = res.obj?.removed ?? 0;
        setRulesResult({
          success: true,
          msg: `${t('pages.settings.routerosNatCleanupSuccess')} (${removed})`,
        });
        await loadRules();
      } else {
        setRulesResult({
          success: false,
          msg: res?.msg || t('pages.settings.requestFailed'),
        });
      }
    } catch (e: unknown) {
      setRulesResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setCleanupLoading(false);
    }
  }

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

  async function handleDetectWanInterface() {
    setDetectWanLoading(true);
    setResult(null);
    try {
      const res = (await HttpUtil.post(
        '/panel/api/setting/detectRouterosWanInterface',
        {
          host: allSetting.routerosNatHost,
          port: allSetting.routerosNatPort,
          username: allSetting.routerosNatUsername,
          password: allSetting.routerosNatPassword,
        },
        { silent: true },
      )) as { success?: boolean; msg?: string; obj?: { interface?: string } };
      const iface = res?.obj?.interface?.trim() ?? '';
      if (res?.success && iface) {
        updateSetting({ routerosNatInInterface: iface });
      } else {
        setResult({
          success: false,
          msg: res?.msg || t('pages.settings.routerosNatDetectWanFailed'),
        });
      }
    } catch (e: unknown) {
      setResult({
        success: false,
        msg: e instanceof Error ? e.message : t('pages.settings.requestFailed'),
      });
    } finally {
      setDetectWanLoading(false);
    }
  }

  async function handleTestConnection() {
    setTestLoading(true);
    setResult(null);
    setHealth(null);
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
        setHealth(res.obj ?? null);
        setResult({
          success: true,
          msg: t('pages.settings.routerosNatTestSuccess'),
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

  const ruleColumns = useMemo<TableColumnsType<ManagedNatRule>>(
    () => [
      {
        title: t('pages.settings.routerosNatColInbound'),
        dataIndex: 'inboundId',
        key: 'inboundId',
        width: 90,
        render: (id: number) => <Tag>#{id}</Tag>,
      },
      {
        title: t('protocol'),
        dataIndex: 'protocol',
        key: 'protocol',
        width: 90,
        render: (p: string) => <Tag color="blue">{p.toUpperCase()}</Tag>,
      },
      {
        title: t('pages.settings.routerosNatColPort'),
        dataIndex: 'dstPort',
        key: 'dstPort',
        width: 90,
      },
      {
        title: t('pages.settings.routerosNatColTarget'),
        key: 'target',
        render: (_: unknown, row: ManagedNatRule) => `${row.toAddress}:${row.toPort}`,
      },
      {
        title: t('pages.settings.routerosNatColMatch'),
        key: 'match',
        render: (_: unknown, row: ManagedNatRule) =>
          row.inInterface ? `in:${row.inInterface}` : row.dstAddressType || 'local',
      },
      {
        title: t('status'),
        dataIndex: 'disabled',
        key: 'disabled',
        width: 110,
        render: (disabled: boolean) => (
          <Tag color={disabled ? 'error' : 'success'}>
            {disabled ? t('disabled') : t('enabled')}
          </Tag>
        ),
      },
      {
        title: t('pages.settings.routerosNatColTraffic'),
        key: 'traffic',
        render: (_: unknown, row: ManagedNatRule) =>
          `${SizeFormatter.sizeFormat(row.bytes)} (${row.packets.toLocaleString()} pkts)`,
      },
    ],
    [t],
  );

  return (
    <Tabs
      defaultActiveKey="1"
      onChange={(key) => {
        if (key === '2') {
          void loadRules();
        }
      }}
      items={[
        {
          key: '1',
          label: catTabLabel(
            <SettingOutlined aria-hidden />,
            t('pages.settings.routerosNatTabSettings'),
            isMobile,
          ),
          children: (
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
                title={t('pages.settings.routerosNatInInterface')}
                description={t('pages.settings.routerosNatInInterfaceDesc')}
              >
                <Space.Compact style={{ width: '100%' }}>
                  <Input
                    aria-label={t('pages.settings.routerosNatInInterface')}
                    value={allSetting.routerosNatInInterface}
                    placeholder="ether1 / pppoe-out1"
                    onChange={(e) => updateSetting({ routerosNatInInterface: e.target.value })}
                  />
                  <Button
                    icon={<RadarChartOutlined aria-hidden />}
                    loading={detectWanLoading}
                    onClick={handleDetectWanInterface}
                  >
                    {t('pages.settings.routerosNatDetectIP')}
                  </Button>
                </Space.Compact>
              </SettingListItem>

              <SettingListItem
                paddings="small"
                title={t('pages.settings.routerosContainerComment')}
                description={t('pages.settings.routerosContainerCommentDesc')}
              >
                <Input
                  value={allSetting.routerosContainerComment}
                  placeholder="[Mikrozia:container] Container"
                  onChange={(e) => updateSetting({ routerosContainerComment: e.target.value })}
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

                {health && (
                  <Descriptions
                    bordered
                    size="small"
                    column={isMobile ? 1 : 2}
                    items={[
                      {
                        key: 'identity',
                        label: t('pages.settings.routerosHealthIdentity'),
                        children: health.identity || '—',
                      },
                      {
                        key: 'version',
                        label: t('pages.settings.routerosHealthVersion'),
                        children:
                          [health.version, health.boardName].filter(Boolean).join(' · ') || '—',
                      },
                      {
                        key: 'disk',
                        label: t('pages.settings.routerosHealthFreeDisk'),
                        children:
                          health.freeHddSpace && health.totalHddSpace
                            ? `${SizeFormatter.sizeFormat(health.freeHddSpace)} / ${SizeFormatter.sizeFormat(health.totalHddSpace)}`
                            : '—',
                      },
                      {
                        key: 'masq',
                        label: t('pages.settings.routerosHealthMasquerade'),
                        children: (
                          <Tag color={health.hasMasquerade ? 'success' : 'warning'}>
                            {health.hasMasquerade
                              ? t('pages.settings.routerosHealthMasqOk')
                              : t('pages.settings.routerosHealthMasqMissing')}
                          </Tag>
                        ),
                      },
                      {
                        key: 'rules',
                        label: t('pages.settings.routerosHealthManagedRules'),
                        children: `${health.managedRules ?? 0} / ${health.natRules ?? 0}`,
                      },
                    ]}
                  />
                )}
              </Space>
            </>
          ),
        },
        {
          key: '2',
          label: catTabLabel(
            <TableOutlined aria-hidden />,
            t('pages.settings.routerosNatTabRules'),
            isMobile,
          ),
          children: (
            <Space orientation="vertical" size={12} style={{ width: '100%' }}>
              <Space wrap>
                <Button
                  icon={<ReloadOutlined aria-hidden />}
                  loading={rulesLoading}
                  onClick={() => void loadRules()}
                >
                  {t('refresh')}
                </Button>
                <Button
                  danger
                  icon={<DeleteOutlined aria-hidden />}
                  loading={cleanupLoading}
                  disabled={!allSetting.routerosNatEnable}
                  onClick={handleCleanupOrphans}
                >
                  {t('pages.settings.routerosNatCleanupOrphans')}
                </Button>
              </Space>

              {rulesResult && (
                <Alert
                  type={rulesResult.success ? 'success' : 'error'}
                  title={rulesResult.msg}
                  showIcon
                  closable={{ onClose: () => setRulesResult(null) }}
                />
              )}

              <Table<ManagedNatRule>
                rowKey="id"
                size="small"
                loading={rulesLoading}
                columns={ruleColumns}
                dataSource={rules}
                pagination={false}
                scroll={{ x: 'max-content' }}
                locale={{ emptyText: t('pages.settings.routerosNatNoRules') }}
              />
            </Space>
          ),
        },
        {
          key: '3',
          label: catTabLabel(
            <BookOutlined aria-hidden />,
            t('pages.settings.routerosNatTabGuide'),
            isMobile,
          ),
          children: (
            <Space orientation="vertical" size={12} style={{ width: '100%' }}>
              <Typography.Text strong>
                {t('pages.settings.routerosNatGuideStep1Title')}
              </Typography.Text>
              <Typography.Text type="secondary">
                {t('pages.settings.routerosNatGuideStep1Desc')}
              </Typography.Text>
              <Alert
                type="warning"
                showIcon
                title={t('pages.settings.routerosNatPasswordCharsWarn')}
              />
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
  );
}
