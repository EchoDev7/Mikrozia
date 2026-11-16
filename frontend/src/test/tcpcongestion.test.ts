import { describe, expect, it } from 'vitest';

import { rawInboundToFormValues } from '@/lib/xray/inbound-form-adapter';
import { formValuesToWirePayload as outboundFormValuesToWirePayload } from '@/lib/xray/outbound-form-adapter';
import { normalizeSockoptForWire } from '@/lib/xray/stream-wire-normalize';
import {
  SockoptStreamSettingsSchema,
  TcpCongestionSchema,
} from '@/schemas/protocols/stream/sockopt';

describe('TcpCongestionSchema and Sockopt defaults (RouterOS contract)', () => {
  it('parse({}) does not inject tcpcongestion=bbr', () => {
    const parsed = SockoptStreamSettingsSchema.parse({});
    expect(parsed.tcpcongestion).toBeUndefined();
    expect('tcpcongestion' in parsed && parsed.tcpcongestion !== undefined).toBe(false);
  });

  it('normalizes empty string to undefined and preserves bbr, cubic, reno', () => {
    expect(TcpCongestionSchema.parse(undefined)).toBeUndefined();
    expect(TcpCongestionSchema.parse('')).toBeUndefined();
    expect(TcpCongestionSchema.parse('   ')).toBeUndefined();
    expect(TcpCongestionSchema.parse('bbr')).toBe('bbr');
    expect(TcpCongestionSchema.parse('cubic')).toBe('cubic');
    expect(TcpCongestionSchema.parse('reno')).toBe('reno');
    expect(() => TcpCongestionSchema.parse('invalid-algo')).toThrow();
  });

  it('wire normalizer omits undefined/empty tcpcongestion and preserves explicit values', () => {
    expect(normalizeSockoptForWire({ tcpcongestion: undefined, tcpFastOpen: true })).toEqual({
      tcpFastOpen: true,
    });
    expect(normalizeSockoptForWire({ tcpcongestion: '', tcpFastOpen: true })).toEqual({
      tcpFastOpen: true,
    });
    expect(normalizeSockoptForWire({ tcpcongestion: 'bbr', tcpUserTimeout: 10000 })).toEqual({
      tcpcongestion: 'bbr',
      tcpUserTimeout: 10000,
    });
    expect(normalizeSockoptForWire({ tcpcongestion: 'cubic' })).toEqual({
      tcpcongestion: 'cubic',
    });
  });

  it('non-stream outbound adapter omits empty tcpcongestion from wire output', () => {
    const wire = outboundFormValuesToWirePayload({
      protocol: 'freedom',
      tag: 'direct',
      settings: { domainStrategy: 'AsIs' },
      streamSettings: {
        sockopt: {
          tcpcongestion: '' as unknown as undefined,
          tcpUserTimeout: 5000,
        },
      },
    } as never);
    const sockopt = (wire.streamSettings as Record<string, unknown>)?.sockopt as Record<
      string,
      unknown
    >;
    expect(sockopt).toBeDefined();
    expect('tcpcongestion' in sockopt).toBe(false);
    expect(sockopt.tcpUserTimeout).toBe(5000);
  });

  it('inbound form adapter does not inject tcpcongestion=bbr when missing from stored sockopt', () => {
    const form = rawInboundToFormValues({
      remark: 'test',
      enable: true,
      expiryTime: 0,
      listen: '',
      port: 443,
      protocol: 'vless',
      settings: '{"clients":[],"decryption":"none"}',
      streamSettings: '{"network":"tcp","sockopt":{"tcpFastOpen":true}}',
      tag: 'in-1',
      sniffing: '{"enabled":false}',
    });
    expect(form.streamSettings?.sockopt?.tcpcongestion).toBeUndefined();
  });
});
