import { useTranslation } from 'react-i18next';
import { Card } from 'antd';
import { ApiOutlined } from '@ant-design/icons';

import { SizeFormatter } from '@/utils';
import type { TopInbound } from '@/generated/types';

interface TopInboundsTileProps {
  inbounds?: TopInbound[];
  isLoading: boolean;
  isError: boolean;
}

const MAX_ROWS = 5;

function inboundDisplayName(inbound: TopInbound): string {
  const trimmed = inbound.remark.trim();
  return trimmed || `${inbound.protocol}:${inbound.port}`;
}

export default function TopInboundsTile({ inbounds, isLoading, isError }: TopInboundsTileProps) {
  const { t } = useTranslation();
  const ready = !isError && !isLoading && inbounds !== undefined;
  const rows = ready ? inbounds.slice(0, MAX_ROWS) : [];

  return (
    <Card hoverable className="ov-tile" styles={{ body: { padding: 0 } }}>
      <div className="ov-tile-head">
        <span className="ov-tile-icon">
          <ApiOutlined aria-hidden />
        </span>
        <span className="ov-kicker">{t('pages.index.topInbounds')}</span>
      </div>

      {isError ? (
        <div className="ov-tile-value">
          <span className="ov-clients-unavailable">{t('pages.index.unavailable')}</span>
        </div>
      ) : !ready ? (
        <>
          <div className="ov-tile-value">
            <span className="ov-tile-number">—</span>
          </div>
          <div className="ov-top-inbounds-list" aria-hidden>
            {Array.from({ length: MAX_ROWS }, (_, idx) => (
              <div key={idx} className="ov-top-inbounds-row ov-top-inbounds-placeholder">
                <span className="ov-top-inbounds-main">
                  <span className="ov-top-inbounds-rank">{idx + 1}</span>
                  <span className="ov-top-inbounds-name">—</span>
                </span>
                <span className="ov-top-inbounds-traffic">—</span>
              </div>
            ))}
          </div>
        </>
      ) : rows.length === 0 ? (
        <div className="ov-top-inbounds-empty">{t('pages.index.noInbounds')}</div>
      ) : (
        <div className="ov-top-inbounds-list">
          {rows.map((inbound, idx) => (
            <div key={inbound.id} className="ov-top-inbounds-row">
              <span className="ov-top-inbounds-main">
                <span className="ov-top-inbounds-rank">{idx + 1}</span>
                <span className="ov-top-inbounds-name">{inboundDisplayName(inbound)}</span>
                {!inbound.enabled && (
                  <span className="ov-top-inbounds-disabled">{t('disabled')}</span>
                )}
              </span>
              <span className="ov-top-inbounds-traffic">
                {SizeFormatter.sizeFormat(inbound.traffic)}
              </span>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}
