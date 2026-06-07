import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogInstancePanel } from './WatchdogInstancePanel';
import type { WatchdogRollup } from '@/lib/api/watchdogRollups';

const enabled: WatchdogRollup = {
  instance: 'homelab',
  enabled: true,
  active: true,
  watched: 12,
  unregistered: 2,
  regrabs_24h: 1,
  regrabs_7d: 5,
  blacklist_size: 3,
  qbit_reachable: true,
  poll_interval_min: 30,
  regrab_cooldown_h: 120,
  max_no_better: 3,
};
const disabled: WatchdogRollup = {
  ...enabled,
  instance: '4k',
  enabled: false,
  active: false,
  watched: 0,
  qbit_reachable: false,
};

let fetchSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchSpy = vi.fn(async () =>
    new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
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
  it('renders the active card with chips + sparkline', () => {
    render(wrap(<WatchdogInstancePanel rollup={enabled} sparkline={[1, 2, 0, 3, 1, 0, 2]} />));
    expect(screen.getByTestId('watchdog-panel-homelab')).toBeInTheDocument();
    expect(screen.getByLabelText('regrab-sparkline-homelab')).toBeInTheDocument();
  });

  it('toggle fires PUT /qbit/settings on user click', async () => {
    const u = userEvent.setup();
    render(wrap(<WatchdogInstancePanel rollup={enabled} />));
    await u.click(screen.getByTestId('watchdog-panel-toggle-homelab'));
    expect(fetchSpy).toHaveBeenCalled();
    const call = fetchSpy.mock.calls.find(([url]) =>
      String(url).includes('/qbit/settings'),
    );
    expect(call).toBeDefined();
    expect(call![1]?.method).toBe('PUT');
    expect(JSON.parse(String(call![1]?.body))).toMatchObject({ enabled: false });
  });

  it('disabled rollup shows the enable CTA and disabled copy', () => {
    render(wrap(<WatchdogInstancePanel rollup={disabled} />));
    expect(screen.getByTestId('watchdog-panel-enable-4k')).toBeInTheDocument();
  });
});
