import { describe, it, expect } from 'vitest';

import { parseLogLine } from '@/pages/index/logParse';

describe('parseLogLine — application log format', () => {
  it('parses "YYYY/MM/DD HH:MM:SS LEVEL - body" and tags X-UI', () => {
    const r = parseLogLine(
      '2026/06/09 00:35:09 INFO - mtproto: started mtg for inbound 3 on 0.0.0.0:8443',
    );
    expect(r.date).toBe('2026/06/09');
    expect(r.time).toBe('00:35:09');
    expect(r.stamp).toBe('2026/06/09 00:35:09');
    expect(r.levelText).toBe('INFO');
    expect(r.levelClass).toBe('level-info');
    expect(r.service).toBe('X-UI:');
    expect(r.body).toBe('mtproto: started mtg for inbound 3 on 0.0.0.0:8443');
  });

  it('lifts the XRAY service tag when body starts with XRAY:', () => {
    const r = parseLogLine('2026/06/09 00:35:09 WARNING - XRAY: core: Xray 26.6.1 started');
    expect(r.levelText).toBe('WARNING');
    expect(r.levelClass).toBe('level-warning');
    expect(r.service).toBe('XRAY:');
    expect(r.body).toBe('core: Xray 26.6.1 started');
  });

  it('maps ERROR level to level-error', () => {
    const r = parseLogLine('2026/06/09 00:35:09 ERROR - bind failed');
    expect(r.levelText).toBe('ERROR');
    expect(r.levelClass).toBe('level-error');
    expect(r.service).toBe('X-UI:');
    expect(r.body).toBe('bind failed');
  });

  it('keeps plain messages verbatim and handles empty lines', () => {
    const plain = parseLogLine('plain notice');
    expect(plain.stamp).toBe('');
    expect(plain.levelClass).toBe('level-unknown');
    expect(plain.body).toBe('plain notice');

    const empty = parseLogLine('');
    expect(empty.stamp).toBe('');
    expect(empty.body).toBe('');
  });
});
