import { Form } from 'antd';
import { FormProvider, useForm } from 'react-hook-form';
import { describe, expect, it, vi } from 'vitest';

import AmneziawgFields from '@/pages/inbounds/form/protocols/amneziawg';
import { createDefaultAmneziawgInboundSettings } from '@/lib/xray/inbound-defaults';
import { AmneziawgServerSchema } from '@/schemas/protocols/inbound/amneziawg';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

interface FormValues {
  nodeId: number | null;
  settings: {
    server: Record<string, unknown>;
  };
}

function Harness() {
  const [antdForm] = Form.useForm();
  const form = useForm<FormValues>({
    defaultValues: {
      nodeId: null,
      settings: {
        server: {
          privateKey: 'priv',
          publicKey: 'pub',
          subnetIp: '10.8.1.0',
          subnetCidr: 24,
          primaryDns: '8.8.8.8',
          secondaryDns: '8.8.4.4',
          externalInterface: '',
          ipv6Enabled: false,
          ipv6Subnet: '',
          ipv6ExternalInterface: '',
        },
      },
    },
  });
  return (
    <FormProvider {...form}>
      <Form form={antdForm} component="div">
        <AmneziawgFields
          awgPubKey="pub"
          regenInboundAwg={() => {}}
          regenInboundAwgObfuscation={() => {}}
        />
      </Form>
    </FormProvider>
  );
}

describe('AmneziaWG userspace-only form and schema', () => {
  it('renders userspace fields without kernel readiness probe or toggle', () => {
    const getSpy = vi.spyOn(HttpUtil, 'get');
    const { getAllByRole, queryByRole, queryByText } = renderWithProviders(<Harness />);

    expect(getSpy).not.toHaveBeenCalled();
    expect(queryByRole('alert')).toBeNull();
    expect(queryByText(/kernelMode|Kernel mode/i)).toBeNull();
    expect(queryByText(/kernel module|kernelUnavailable/i)).toBeNull();
    expect(queryByText('Enable IPv6')).not.toBeNull();
    // ipv6Enabled, randomTrailers, disableCookies — no kernelMode switch
    expect(getAllByRole('switch')).toHaveLength(3);
  });

  it('omits kernelMode from inbound defaults and zod schema', () => {
    const defaults = createDefaultAmneziawgInboundSettings();
    expect('kernelMode' in defaults.server).toBe(false);
    expect('kernelMode' in AmneziawgServerSchema.shape).toBe(false);
  });
});
