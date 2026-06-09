import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogInstancePanel } from './WatchdogInstancePanel';
import type { WatchdogRollup } from '@/lib/api/watchdogRollups';

const enabled: WatchdogRollup = {
  instance_name: 'homelab', enabled: true, active: true,
  watched: 12, unregistered: 2, regrabs_24h: 1, regrabs_7d: 5,
  blacklist_size: 3, qbit_reachable: true,
  poll_interval_seconds: 1800, cooldown_hours: 120, no_better_max: 3,
};
const disabled: WatchdogRollup = {
  ...enabled, instance_name: '4k', enabled: false, active: false,
  watched: 0, qbit_reachable: false,
};

const qbitDTO = {
  id: 1, instance_id: 7, instance_name: 'homelab', enabled: true,
  url: 'http://qbit.local:8080', username: 'admin', password_set: true,
  category: 'sonarr', poll_interval_minutes: 30,
  regrab_cooldown_hours: 120, max_consecutive_no_better: 3,
  custom_unregistered_msgs: [] as string[],
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

let fetchSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchSpy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? 'GET').toUpperCase();
    if (url.includes('/qbit/settings') && method === 'GET') {
      return new Response(JSON.stringify(qbitDTO), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('{}', {
      status: 200, headers: { 'Content-Type': 'application/json' },
    });
  });
  vi.stubGlobal('fetch', fetchSpy);
});

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<WatchdogInstancePanel />', () => {
  it('toggle PUT body carries url + category + _minutes/_hours field names', async () => {
    const u = userEvent.setup();
    render(wrap(<WatchdogInstancePanel rollup={enabled} />));
    await waitFor(() => {
      expect(screen.getByTestId('watchdog-panel-toggle-homelab')).not.toBeDisabled();
    });
    await u.click(screen.getByTestId('watchdog-panel-toggle-homelab'));
    await waitFor(() => {
      const put = fetchSpy.mock.calls.find(
        ([, init]) => (init as RequestInit | undefined)?.method === 'PUT',
      );
      expect(put).toBeDefined();
      const body = JSON.parse(String((put![1] as RequestInit).body));
      expect(body.enabled).toBe(false);
      expect(body.url).toBe('http://qbit.local:8080');
      expect(body.category).toBe('sonarr');
      expect(body.poll_interval_minutes).toBe(30);
      expect(body.regrab_cooldown_hours).toBe(120);
      expect(body.max_consecutive_no_better).toBe(3);
    });
  });

  it('toggle is disabled while qBit settings query is pending', () => {
    fetchSpy.mockImplementation(async () => new Promise<Response>(() => {}));
    render(wrap(<WatchdogInstancePanel rollup={enabled} />));
    expect(screen.getByTestId('watchdog-panel-toggle-homelab')).toBeDisabled();
  });

  it('"Настроить" calls onOpenInstanceForm with the instance name', async () => {
    const u = userEvent.setup();
    const cb = vi.fn();
    render(wrap(<WatchdogInstancePanel rollup={enabled} onOpenInstanceForm={cb} />));
    await u.click(screen.getByTestId('watchdog-panel-configure-homelab'));
    expect(cb).toHaveBeenCalledWith('homelab');
  });

  it('disabled rollup: enable CTA fires onOpenInstanceForm', async () => {
    const u = userEvent.setup();
    const cb = vi.fn();
    render(wrap(<WatchdogInstancePanel rollup={disabled} onOpenInstanceForm={cb} />));
    await u.click(screen.getByTestId('watchdog-panel-enable-4k'));
    expect(cb).toHaveBeenCalledWith('4k');
  });

  // Regression guard for Story 090 Bug 1: prior frontend type used
  // `instance` but the API returns `instance_name`. Clicking "Настроить"
  // navigated to `/instances?edit=undefined`. This test forces a wire-
  // shaped object through the component and asserts the callback never
  // receives undefined.
  it('Bug 1 regression: configure click never passes undefined', async () => {
    const u = userEvent.setup();
    const cb = vi.fn();
    const wireRollup = {
      instance_name: 'homelab', enabled: true, active: true,
      watched: 0, unregistered: 0, regrabs_24h: 0, regrabs_7d: 0,
      blacklist_size: 0, qbit_reachable: false,
      poll_interval_seconds: 1800, cooldown_hours: 120, no_better_max: 3,
    } satisfies WatchdogRollup;
    render(wrap(<WatchdogInstancePanel rollup={wireRollup} onOpenInstanceForm={cb} />));
    await u.click(screen.getByTestId('watchdog-panel-configure-homelab'));
    expect(cb).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledWith('homelab');
    expect(cb).not.toHaveBeenCalledWith(undefined);
  });
});
