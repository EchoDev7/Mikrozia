import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Tooltip } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined, InfoCircleOutlined } from '@ant-design/icons';

import { SizeFormatter } from '@/utils';
import { Sparkline } from '@/components/viz';
import type { Status } from '@/models/status';
import { mean, peak } from './useOverviewHistory';

const LIVE_WINDOW_POINTS = 30; // 30 samples × 2s cadence = last 60s
const UPLOAD_COLOR = '#10b981';
const DOWNLOAD_COLOR = '#2563eb';

interface ThroughputCardProps {
  status: Status;
  up: Array<number | null>;
  down: Array<number | null>;
  labels: string[];
  isMobile: boolean;
}

export default function ThroughputCard({
  status,
  up,
  down,
  labels,
  isMobile,
}: ThroughputCardProps) {
  const { t } = useTranslation();
  const available = status.metricAvailable('netIO');

  const liveUp = useMemo(() => up.slice(-LIVE_WINDOW_POINTS), [up]);
  const liveDown = useMemo(() => down.slice(-LIVE_WINDOW_POINTS), [down]);
  const liveLabels = useMemo(() => labels.slice(-LIVE_WINDOW_POINTS), [labels]);

  const referenceLines = useMemo(
    () =>
      available
        ? [
            { y: status.netIO.down, color: DOWNLOAD_COLOR, dash: '2 4' },
            { y: status.netIO.up, color: UPLOAD_COLOR, dash: '2 4' },
          ]
        : [],
    [available, status.netIO.up, status.netIO.down],
  );

  return (
    <Card hoverable styles={{ body: { padding: 0 } }}>
      <div className="ov-wide-head">
        <div>
          <div className="ov-kicker ov-kicker-row">
            <span>{t('pages.index.overallSpeed')}</span>
            {available && (
              <span className="ov-live-pill">
                <span className="ov-live-dot" aria-hidden />
                LIVE · 60s
              </span>
            )}
            <Tooltip
              title={t('pages.index.overallSpeedHint')}
              trigger={['hover', 'focus', 'click']}
            >
              <button
                type="button"
                className="ov-kicker-hint"
                aria-label={t('pages.index.overallSpeedHint')}
              >
                <InfoCircleOutlined aria-hidden />
              </button>
            </Tooltip>
          </div>
          <div className="ov-sub">
            {`${t('pages.index.throughputSub')} · ${t('pages.index.peak')} ${
              available
                ? `↓ ${SizeFormatter.speedMbpsFormat(peak(liveDown))} · ↑ ${SizeFormatter.speedMbpsFormat(peak(liveUp))}`
                : '—'
            }`}
          </div>
        </div>
        <div className="ov-wide-legend">
          <div className="ov-legend-label">
            <ArrowUpOutlined aria-hidden style={{ color: UPLOAD_COLOR }} />
            {t('pages.index.upload')}
            <span className="ov-legend-num" style={{ color: available ? UPLOAD_COLOR : undefined }}>
              {available
                ? SizeFormatter.speedMbpsFormat(status.netIO.up)
                : t('pages.index.unavailable')}
            </span>
          </div>
          <div className="ov-legend-label">
            <ArrowDownOutlined aria-hidden style={{ color: DOWNLOAD_COLOR }} />
            {t('pages.index.download')}
            <span
              className="ov-legend-num"
              style={{ color: available ? DOWNLOAD_COLOR : undefined }}
            >
              {available
                ? SizeFormatter.speedMbpsFormat(status.netIO.down)
                : t('pages.index.unavailable')}
            </span>
          </div>
        </div>
      </div>

      <div className="ov-wide-chart ov-wide-chart--padded">
        <Sparkline
          data={liveUp}
          data2={liveDown}
          labels={liveLabels}
          height={isMobile ? 150 : 194}
          strokeWidth={2}
          fillOpacity={0.18}
          maxPoints={LIVE_WINDOW_POINTS}
          showGrid
          showAxes
          showMarker
          markerRadius={3}
          showTooltip
          showLegend={false}
          valueMax={null}
          stroke={UPLOAD_COLOR}
          stroke2={DOWNLOAD_COLOR}
          name1={t('pages.index.upload')}
          name2={t('pages.index.download')}
          yFormatter={SizeFormatter.speedMbpsFormat}
          referenceLines={referenceLines}
        />
      </div>

      <div className="ov-wide-foot">
        <div>
          <div className="ov-kicker">{t('pages.index.sent')}</div>
          <div className="ov-foot-value">
            {available ? SizeFormatter.sizeFormat(status.netTraffic.sent) : '—'}
          </div>
        </div>
        <span className="ov-foot-sep" />
        <div>
          <div className="ov-kicker">{t('pages.index.received')}</div>
          <div className="ov-foot-value">
            {available ? SizeFormatter.sizeFormat(status.netTraffic.recv) : '—'}
          </div>
        </div>
        <span className="ov-foot-sep" />
        <div>
          <div className="ov-kicker">{t('pages.index.avgWindow')}</div>
          <div className="ov-foot-value">
            {available ? (
              <>
                <span className="ov-foot-part">{`↑ ${SizeFormatter.speedMbpsFormat(mean(liveUp))}`}</span>{' '}
                <span className="ov-foot-part">{`↓ ${SizeFormatter.speedMbpsFormat(mean(liveDown))}`}</span>
              </>
            ) : (
              '—'
            )}
          </div>
        </div>
      </div>
    </Card>
  );
}
