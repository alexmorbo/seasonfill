import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogActivityFeed } from './WatchdogActivityFeed';

const grabsFixture = {
  items: [
    {
      id: 'g1',
      instance: 'homelab',
      series_id: 11,
      series_title: 'Foundation',
      season_number: 3,
      created_at: new Date('2026-06-07T04:28:00Z').toISOString(),
      status: 'imported',
      replay_of_id: 'g0',
      custom_format_score: 100,
      parent_custom_format_score: 50,
      episodes_count: 6,
    },
    {
      id: 'g2',
      instance: 'homelab',
      series_id: 22,
      series_title: 'Wednesday',
      season_number: 2,
      created_at: new Date('2026-06-06T22:00:00Z').toISOString(),
      status: 'imported',
      replay_of_id: 'gx',
      custom_format_score: 30,
      parent_custom_format_score: 40,
      consecutive_no_better: 2,
    },
    {
      // plain grab (no replay) — Story 098b emits as type=grab
      id: 'g3',
      instance: 'homelab',
      series_id: 33,
      series_title: 'Severance',
      season_number: 3,
      created_at: new Date('2026-06-07T08:00:00Z').toISOString(),
      status: 'imported',
      release_title: 'Severance.S03E01.2160p',
      episodes_count: 1,
    },
  ],
};
const decisionsFixture = {
  items: [
    {
      id: 'd1',
      instance: 'homelab',
      series_id: 11,
      series_title: 'Foundation',
      season_number: 3,
      created_at: new Date('2026-06-07T05:00:00Z').toISOString(),
      decision: 'grab',
      reason: 'upgrade_available',
    },
    {
      // skip rows are dropped by the hook filter
      id: 'd2',
      instance: 'homelab',
      series_id: 11,
      series_title: 'Foundation',
      season_number: 3,
      created_at: new Date('2026-06-07T04:00:00Z').toISOString(),
      decision: 'skip',
      reason: 'no_change',
    },
  ],
};
const blFixture = {
  items: [
    {
      id: 7,
      instance: 'homelab',
      series_id: 22,
      series_title: 'Wednesday',
      season_number: 2,
      reason: 'auto_no_better_threshold' as const,
      consecutive: 3,
      created_at: new Date('2026-06-07T06:12:00Z').toISOString(),
    },
  ],
};

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.includes('/decisions')) {
        return new Response(JSON.stringify(decisionsFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/grabs')) {
        return new Response(JSON.stringify(grabsFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.includes('/watchdog/blacklist')) {
        return new Response(JSON.stringify(blFixture), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response('{}', { status: 404 });
    }),
  );
});

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>{ui}</MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<WatchdogActivityFeed />', () => {
  it('renders derived events sorted by time desc', async () => {
    render(wrap(<WatchdogActivityFeed instance="homelab" maxNoBetter={3} />));
    expect(await screen.findByTestId('watchdog-activity-feed')).toBeInTheDocument();
    expect(await screen.findByTestId('feed-row-blacklist')).toBeInTheDocument();
    expect(await screen.findAllByTestId('feed-row-regrab')).toHaveLength(2);
    expect(await screen.findByTestId('feed-row-better')).toBeInTheDocument();
    expect(await screen.findByTestId('feed-row-no_better')).toBeInTheDocument();
  });

  it('emits plain "grab" rows for non-replay grabs and "decision" rows for non-skip outcomes', async () => {
    render(wrap(<WatchdogActivityFeed instance="homelab" maxNoBetter={3} />));
    expect(await screen.findByTestId('feed-row-grab')).toBeInTheDocument();
    expect(await screen.findByTestId('feed-row-decision')).toBeInTheDocument();
  });

  it('renders the placeholder when both queries are empty', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    render(wrap(<WatchdogActivityFeed instance="homelab" />));
    expect(
      await screen.findByText(/Events will appear|События будут/i),
    ).toBeInTheDocument();
  });
});
