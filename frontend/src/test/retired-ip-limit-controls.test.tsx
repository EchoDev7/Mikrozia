import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it, vi } from 'vitest';

import ClientFormModal from '@/pages/clients/ClientFormModal';
import { fieldLabels, renderWithProviders } from './test-utils';

/*
 * RouterOS Container cannot run Fail2ban or change host firewall rules, so IP
 * limits are not enforced. The panel must not offer a control or a status
 * promise for enforcement it cannot perform; the stored limit values survive
 * only as compatibility data.
 */

const srcRoot = resolve(process.cwd(), 'src');
const selfPath = fileURLToPath(import.meta.url);

function collectSources(dir: string, out: string[]): void {
  for (const entry of readdirSync(dir)) {
    if (['node_modules', 'dist', 'storybook-static'].includes(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) collectSources(full, out);
    else if ((entry.endsWith('.ts') || entry.endsWith('.tsx')) && resolve(full) !== selfPath)
      out.push(full);
  }
}

describe('retired IP-limit enforcement controls', () => {
  it('client form offers no IP limit input and no Fail2ban notice', () => {
    renderWithProviders(
      <ClientFormModal
        open
        mode="add"
        client={null}
        inbounds={[]}
        save={vi.fn().mockResolvedValue(null)}
        onOpenChange={() => {}}
      />,
    );

    expect(fieldLabels()).not.toContain('IP Limit');
    expect(document.body.textContent ?? '').not.toContain('Fail2ban');
  });

  it('no frontend source references the retired status API or allowlist setting', () => {
    const sources: string[] = [];
    collectSources(srcRoot, sources);
    const offenders: string[] = [];
    for (const file of sources) {
      const text = readFileSync(file, 'utf8');
      for (const retired of [
        'useFail2banStatusQuery',
        'getLimitIpNotice',
        'fail2banStatus',
        'ipLimitAllowlist',
      ]) {
        if (text.includes(retired)) offenders.push(`${file}: ${retired}`);
      }
    }
    expect(offenders, `retired Fail2ban/IP-limit references:\n  ${offenders.join('\n  ')}`).toEqual(
      [],
    );
  });
});
