import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import i18n from '@/i18n';
import { Calendar } from './Calendar';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}
function ymd(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

// A day guaranteed to fall inside the current Mon..Sun week, and one far ahead.
const today = ymd(new Date());
const nextMonth = ymd(new Date(new Date().getFullYear(), new Date().getMonth() + 2, 15));

function report() {
  return {
    generated_at: new Date().toISOString(),
    from: `${today}T00:00:00Z`,
    to: `${nextMonth}T00:00:00Z`,
    days: [
      {
        date: today,
        events: [
          {
            series_id: 42,
            title: 'The Expanse',
            season: 2,
            episode: 1,
            air_date: `${today}T00:00:00Z`,
            state: 'downloaded',
            in_library_instances: ['main'],
            season_premiere: true,
            milestone: 'premiere',
            media_type: 'tv',
          },
        ],
      },
      {
        date: nextMonth,
        events: [
          {
            series_id: 77,
            title: 'Severance',
            season: 1,
            episode: 9,
            air_date: `${nextMonth}T00:00:00Z`,
            state: 'missing',
            in_library_instances: ['main'],
            season_premiere: false,
            milestone: 'finale',
            media_type: 'tv',
          },
          {
            series_id: 88,
            title: 'Foundation',
            season: 3,
            episode: 2,
            air_date: `${nextMonth}T00:00:00Z`,
            state: 'upcoming',
            in_library_instances: [],
            season_premiere: false,
            media_type: 'tv',
          },
        ],
      },
    ],
  };
}

// fetch spy that records every requested URL and returns the same report.
function spyFetch(body: unknown = report(), status = 200) {
  const urls: string[] = [];
  const fn = vi.fn(async (url: RequestInfo | URL) => {
    urls.push(typeof url === 'string' ? url : url.toString());
    return json(body, status);
  });
  globalThis.fetch = fn as typeof fetch;
  return urls;
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/calendar', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<Calendar />', () => {
  it('renders the agenda with this-week + upcoming sections, milestones and status dots', async () => {
    spyFetch();
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });

    expect(await screen.findByTestId('calendar-page')).toBeInTheDocument();
    expect(await screen.findByTestId('calendar-agenda')).toBeInTheDocument();
    expect(screen.getByTestId('calendar-this-week')).toBeInTheDocument();
    expect(screen.getByTestId('calendar-upcoming')).toBeInTheDocument();

    // premiere milestone label + a downloaded status dot in this-week
    expect(screen.getByTestId('calendar-milestone-premiere')).toHaveTextContent(
      i18n.t('calendar.milestone.premiere'),
    );
    expect(screen.getByTestId('calendar-state-downloaded')).toBeInTheDocument();

    // finale + missing in the upcoming section
    expect(screen.getByTestId('calendar-milestone-finale')).toBeInTheDocument();
    expect(screen.getByTestId('calendar-state-missing')).toBeInTheDocument();
    expect(screen.getByTestId('calendar-state-upcoming')).toBeInTheDocument();

    // event rows carry the stable series/episode testid
    expect(screen.getByTestId('calendar-event-42-S02E01')).toBeInTheDocument();
  });

  it('switches from agenda to the month grid via the view toggle', async () => {
    spyFetch();
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });

    expect(await screen.findByTestId('calendar-agenda')).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('calendar-view-month'));

    expect(await screen.findByTestId('calendar-month-grid')).toBeInTheDocument();
    expect(screen.getByTestId('calendar-month-label')).toBeInTheDocument();
    expect(screen.queryByTestId('calendar-agenda')).toBeNull();
  });

  it('re-queries with only-premieres=true when the filter toggles', async () => {
    const urls = spyFetch();
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });

    await screen.findByTestId('calendar-agenda');
    await userEvent.click(screen.getByTestId('calendar-filter-only-premieres'));

    await vi.waitFor(() => {
      expect(urls.some((u) => u.includes('only-premieres=true'))).toBe(true);
    });
  });

  it('shows the empty state when the window has no events', async () => {
    spyFetch({ generated_at: new Date().toISOString(), days: [] });
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });

    expect(await screen.findByTestId('calendar-empty')).toBeInTheDocument();
  });

  it('renders the error state with a retry action on failure', async () => {
    spyFetch({ error: 'boom' }, 500);
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });

    expect(await screen.findByTestId('calendar-error')).toBeInTheDocument();
    expect(screen.getByText(i18n.t('common.retry'))).toBeInTheDocument();
  });

  it('shows the loading skeleton while the request is in flight', () => {
    globalThis.fetch = vi.fn().mockReturnValue(new Promise(() => {})) as typeof fetch;
    renderWithProviders(wrap(<Calendar />), { route: '/calendar' });
    expect(screen.getByTestId('calendar-loading')).toBeInTheDocument();
  });
});
