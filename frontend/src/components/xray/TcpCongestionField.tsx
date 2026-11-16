import { Alert, Select } from 'antd';
import type { CSSProperties } from 'react';
import { useFormContext, useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';

import { FormField } from '@/components/form/rhf';
import { TCP_CONGESTION_OPTION } from '@/schemas/primitives';

interface TcpCongestionFieldProps {
  name: string[];
  id?: string;
  style?: CSSProperties;
}

const tcpCongestionTransform = {
  input: (value: unknown) => (typeof value === 'string' && value.trim() !== '' ? value : ''),
  output: (value: unknown) =>
    typeof value === 'string' && value.trim() !== '' ? value : undefined,
};

export function TcpCongestionField({ name, id, style }: TcpCongestionFieldProps) {
  const { t } = useTranslation();
  const { control } = useFormContext();
  const fieldPath = name.join('.');
  const rawValue = useWatch({ control, name: fieldPath }) as string | undefined;
  const currentValue =
    typeof rawValue === 'string' && rawValue.trim() !== '' ? rawValue : undefined;

  const options = [
    { value: '', label: t('pages.inbounds.form.tcpCongestionOsDefault') },
    { value: TCP_CONGESTION_OPTION.CUBIC, label: TCP_CONGESTION_OPTION.CUBIC },
    { value: TCP_CONGESTION_OPTION.RENO, label: TCP_CONGESTION_OPTION.RENO },
  ];
  if (currentValue === TCP_CONGESTION_OPTION.BBR) {
    options.push({
      value: TCP_CONGESTION_OPTION.BBR,
      label: t('pages.inbounds.form.tcpCongestionLegacyBbr'),
    });
  }

  return (
    <>
      <FormField
        name={name}
        label={t('pages.inbounds.form.tcpCongestion')}
        transform={tcpCongestionTransform}
      >
        <Select id={id} style={style} allowClear options={options} />
      </FormField>
      {currentValue === TCP_CONGESTION_OPTION.BBR && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          title={t('pages.inbounds.form.tcpCongestionLegacyBbrWarning')}
        />
      )}
    </>
  );
}
