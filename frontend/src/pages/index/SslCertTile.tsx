import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from 'antd';
import { SafetyCertificateOutlined } from '@ant-design/icons';

import type { TlsCertInfo } from '@/models/status';

interface SslCertTileProps {
  tlsCert: TlsCertInfo | null;
}

export default function SslCertTile({ tlsCert }: SslCertTileProps) {
  const { t, i18n } = useTranslation();

  const expiryText = useMemo(() => {
    if (!tlsCert?.configured || !tlsCert.notAfter) return '—';
    const d = new Date(tlsCert.notAfter * 1000);
    if (Number.isNaN(d.getTime())) return '—';
    try {
      return new Intl.DateTimeFormat(i18n.language || undefined, {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
      }).format(d);
    } catch {
      return d.toISOString().slice(0, 10);
    }
  }, [tlsCert, i18n.language]);

  const configured = Boolean(tlsCert?.configured && tlsCert?.notAfter);
  const expired = Boolean(tlsCert?.expired);
  const daysLeft = tlsCert?.daysLeft ?? 0;

  let statusColor = '#2D6A4F';
  let statusText = t('pages.index.sslCertValid');
  if (!configured) {
    statusColor = 'var(--ant-color-text-tertiary)';
    statusText = t('pages.index.sslCertNotConfigured');
  } else if (expired || daysLeft <= 0) {
    statusColor = '#9B1B30';
    statusText = t('pages.index.sslCertExpired');
  } else if (daysLeft <= 7) {
    statusColor = '#9B1B30';
    statusText = t('pages.index.sslCertExpiringSoon');
  } else if (daysLeft <= 30) {
    statusColor = '#faad14';
    statusText = t('pages.index.sslCertExpiringSoon');
  }

  return (
    <Card className="ov-tile" hoverable styles={{ body: { padding: 0 } }}>
      <div className="ov-tile-head">
        <span className="ov-kicker">
          <SafetyCertificateOutlined aria-hidden />
          {t('pages.index.sslCertificate')}
        </span>
        <span className="ov-tile-detail" title={tlsCert?.subject || ''}>
          {configured ? tlsCert?.subject || 'TLS' : 'HTTP'}
        </span>
      </div>

      <div className="ov-tile-num-row">
        {configured ? (
          <div className="ov-tile-number" style={{ color: statusColor }}>
            {expired ? t('pages.index.sslCertExpired') : `${daysLeft}d`}
          </div>
        ) : (
          <div className="ov-tile-number ov-tile-number--unavailable">—</div>
        )}
      </div>

      <div className="ov-tile-foot">
        <span>{statusText}</span>
        <span>{configured ? `${t('pages.index.sslCertExpires')} ${expiryText}` : '—'}</span>
      </div>
    </Card>
  );
}
