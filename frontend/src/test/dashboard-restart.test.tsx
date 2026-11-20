import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import '@testing-library/jest-dom/vitest';
import { Status } from '@/models/status';
import IndexPage from '@/pages/index/IndexPage';
import { HttpUtil, PromiseUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('@/api/queries/useStatusQuery', () => ({
  useStatusQuery: () => statusResult,
}));
vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));
vi.mock('@/components/sponsor/SponsorSlot', () => ({ default: () => null }));
vi.mock('@/components/viz/Sparkline', () => ({ default: () => null }));

const xrayPath = '/panel/api/server/restartXrayService';
const panelPath = '/panel/api/setting/restartPanel';
const stopPath = '/panel/api/server/stopXrayService';
const accepted = 'Panel restart request submitted.';
const fetchSpy = vi.fn<typeof fetch>();
const statusResult = { status: new Status(), fetched: true, refresh: vi.fn() };

async function openMenu(trigger = 'Restart') {
  fireEvent.click(screen.getByRole('button', { name: trigger }));
  return screen.findByRole('menu');
}

async function choose(name: string, trigger = 'Restart') {
  fireEvent.click(within(await openMenu(trigger)).getByRole('menuitem', { name }));
  return within(await screen.findByRole('dialog'));
}

function writes() {
  return fetchSpy.mock.calls.filter(
    ([url, options]) => options?.method === 'POST' && url !== '/panel/api/setting/defaultSettings',
  );
}

describe('Dashboard lifecycle menus', () => {
  beforeEach(() => {
    vi.mocked(HttpUtil.post).mockRestore?.();
    vi.mocked(HttpUtil.get).mockRestore?.();
    fetchSpy.mockReset().mockImplementation(async (url) => {
      if (url === '/csrf-token') return Response.json({ success: true, obj: 'test-csrf' });
      if (url === panelPath) {
        return Response.json({ success: true, msg: 'The panel was successfully restarted.' });
      }
      if (url === '/panel/api/server/routerosAPIStatus') {
        return Response.json({
          success: true,
          obj: {
            state: 'connected',
            checkedAt: 0,
            stale: false,
            cpuHistory: [],
            memHistory: [],
          },
        });
      }
      return Response.json({ success: true, obj: {} });
    });
    vi.stubGlobal('fetch', fetchSpy);
    renderWithProviders(
      <MemoryRouter>
        <IndexPage />
      </MemoryRouter>,
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('lists exactly three choices and never sends a request for the disabled container', async () => {
    expect(await screen.findByText('RouterOS API: Connected')).toBeInTheDocument();
    const items = within(await openMenu()).getAllByRole('menuitem');
    expect(
      items.map((item) => item.getAttribute('aria-label') || item.textContent?.trim()),
    ).toEqual(['Restart Xray', 'Restart Panel', 'Restart Container']);
    expect(items[2]).toHaveAttribute('aria-disabled', 'true');
    expect(items[2]).toHaveAccessibleDescription(/secure container identification.*RouterOS API/i);
    fireEvent.click(items[2]);
    fireEvent.keyDown(items[2], { key: 'Enter' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(writes()).toHaveLength(0);
  });

  const actions = [
    ['Restart Xray', /active Xray connections will briefly disconnect/i, xrayPath],
    [
      'Restart Panel',
      /panel and Subscription Server.*Xray and RouterOS.*not restart directly/i,
      panelPath,
    ],
  ] as const;

  it('lists Stop choices in order and explains unavailable actions without sending a POST', async () => {
    expect(screen.getByRole('button', { name: 'Restart' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop' })).toHaveTextContent('Stop');
    expect(screen.queryByRole('button', { name: 'Stop Xray' })).toBeNull();
    const items = within(await openMenu('Stop')).getAllByRole('menuitem');
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveAccessibleName('Stop Xray');
    expect(items[0]).not.toHaveAttribute('aria-disabled', 'true');
    expect(items[1]).toHaveAccessibleName('Stop Panel');
    expect(items[1]).toHaveAccessibleDescription(
      'Stopping the panel is not available yet because it would make the panel unreachable. Use Restart Panel when needed.',
    );
    expect(items[2]).toHaveAccessibleName('Stop Container');
    expect(items[2]).toHaveAccessibleDescription(
      'Container stop requires verified RouterOS container identity and lifecycle authorization. It is not available yet.',
    );
    for (const item of items.slice(1)) {
      expect(item).toHaveAttribute('aria-disabled', 'true');
      fireEvent.click(item);
      fireEvent.keyDown(item, { key: 'Enter' });
    }
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(writes()).toHaveLength(0);
  });

  it.each(['Cancel', 'Stop Xray'])('requires confirmation before Stop Xray: %s', async (choice) => {
    const dialog = await choose('Stop Xray', 'Stop');
    expect(
      dialog.getByText(/all Xray inbounds and connected users will be disconnected/i),
    ).toBeInTheDocument();
    expect(writes()).toHaveLength(0);
    fireEvent.click(dialog.getByRole('button', { name: choice }));
    if (choice === 'Cancel') {
      expect(writes()).toHaveLength(0);
    } else {
      await waitFor(() => expect(writes().map(([url]) => url)).toEqual([stopPath]));
    }
  });

  it.each(actions)('warns before %s and sends no POST on cancel', async (name, warning) => {
    const dialog = await choose(name);
    expect(dialog.getByText(warning)).toBeInTheDocument();
    fireEvent.click(dialog.getByRole('button', { name: 'Cancel' }));
    expect(writes()).toHaveLength(0);
  });

  it.each(actions)('sends exactly one POST on confirming %s', async (name, _warning, path) => {
    const delay = vi.spyOn(PromiseUtil, 'sleep').mockImplementation(() => new Promise(() => {}));
    fireEvent.click((await choose(name)).getByRole('button', { name }));
    await waitFor(() => expect(writes().map(([url]) => url)).toEqual([path]));
    if (path === panelPath) {
      expect(new Headers(writes()[0][1]?.headers).get('X-CSRF-Token')).toBe('test-csrf');
      expect(await screen.findByText(accepted)).toBeInTheDocument();
      expect(screen.queryByText('The panel was successfully restarted.')).toBeNull();
      expect(delay).toHaveBeenCalledWith(5000);
    }
  });

  it.each(['403', 'network error'])(
    'does not retry, announce acceptance or schedule reload after a panel POST %s',
    async (failure) => {
      const originalFetch = fetchSpy.getMockImplementation()!;
      fetchSpy.mockImplementation(async (url, options) => {
        if (url !== panelPath) return originalFetch(url, options);
        if (failure === 'network error') throw new TypeError('Network error');
        return Response.json({ success: false }, { status: 403 });
      });
      const delay = vi.spyOn(PromiseUtil, 'sleep');
      fireEvent.click(
        (await choose('Restart Panel')).getByRole('button', { name: 'Restart Panel' }),
      );
      expect(await screen.findByText('Something went wrong')).toBeInTheDocument();
      expect(writes().map(([url]) => url)).toEqual([panelPath]);
      expect(screen.queryByText(accepted)).toBeNull();
      expect(delay).not.toHaveBeenCalled();
    },
  );
});
