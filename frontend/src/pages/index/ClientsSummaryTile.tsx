import { useTranslation } from 'react-i18next';
import { Card } from 'antd';
import { TeamOutlined } from '@ant-design/icons';

import type { ClientsSummaryCounts } from '@/generated/types';

interface ClientsSummaryTileProps {
  summary?: ClientsSummaryCounts;
  isLoading: boolean;
  isError: boolean;
}

export default function ClientsSummaryTile({
  summary,
  isLoading,
  isError,
}: ClientsSummaryTileProps) {
  const { t } = useTranslation();
  const ready = !isError && !isLoading && summary !== undefined;

  const rows = [
    { key: 'online', label: t('online'), dot: '#1677ff', value: summary?.online },
    { key: 'ended', label: t('depleted'), dot: '#ff4d4f', value: summary?.ended },
    { key: 'depleting', label: t('depletingSoon'), dot: '#fa8c16', value: summary?.depleting },
    { key: 'disabled', label: t('disabled'), dot: '#8c8c8c', value: summary?.disabled },
    { key: 'active', label: t('subscription.active'), dot: '#52c41a', value: summary?.active },
  ];

  const totalText = isError
    ? t('pages.index.unavailable')
    : ready
      ? summary.total.toLocaleString()
      : '—';

  return (
    <Card hoverable className="ov-tile" styles={{ body: { padding: 0 } }}>
      <div className="ov-tile-head">
        <span className="ov-tile-icon">
          <TeamOutlined aria-hidden />
        </span>
        <span className="ov-kicker">{t('clients')}</span>
      </div>

      <div className="ov-tile-value">
        <span className={isError ? 'ov-clients-unavailable' : 'ov-tile-number'}>{totalText}</span>
      </div>

      <div className="ov-clients-list">
        {rows.map((row) => (
          <div key={row.key} className="ov-clients-row">
            <span className="ov-clients-label">
              <span className="ov-clients-dot" style={{ backgroundColor: row.dot }} />
              <span>{row.label}</span>
            </span>
            <span className="ov-clients-val">
              {ready && row.value !== undefined ? row.value.toLocaleString() : '—'}
            </span>
          </div>
        ))}
      </div>
    </Card>
  );
}
