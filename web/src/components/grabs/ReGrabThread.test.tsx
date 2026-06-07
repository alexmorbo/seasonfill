import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ReGrabThread } from './ReGrabThread';
import type { Grab } from '@/lib/grabs/chipBuilder';
import { DtoGrabStatus } from '@/api/schema';

function wrap(ui: ReactElement): ReactNode {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </QueryClientProvider>
  );
}

function g(over: Partial<Grab> = {}): Grab {
  return {
    id: 'g_root',
    series_title: 'Foundation',
    series_id: 42,
    season_number: 3,
    status: DtoGrabStatus.grabbed,
    created_at: '2026-06-01T00:00:00Z',
    coverage_count: 1,
    ...over,
  };
}

describe('<ReGrabThread />', () => {
  it('returns null when there is no chain', () => {
    const single = g({});
    const { container } = render(wrap(
      <ReGrabThread instance="alpha" grab={single} all={[single]} open={true} />,
    ));
    expect(container.querySelector('[data-testid^="regrab-thread-"]')).toBeNull();
  });

  it('renders all nodes of a 3-step chain (original → #1 → #2)', () => {
    const root = g({ id: 'g_root', coverage_count: 1, replayed_by: ['g_r1'] });
    const re1 = g({
      id: 'g_r1', coverage_count: 3,
      replay_of_id: 'g_root', replayed_by: ['g_r2'],
      created_at: '2026-06-04T00:00:00Z',
    });
    const re2 = g({
      id: 'g_r2', coverage_count: 6,
      replay_of_id: 'g_r1',
      created_at: '2026-06-07T04:28:00Z',
    });
    render(wrap(
      <ReGrabThread instance="alpha" grab={re2} all={[root, re1, re2]} open={true} />,
    ));
    expect(screen.getByTestId('regrab-thread-g_r2')).toBeInTheDocument();
    expect(screen.getByTestId('regrab-node-g_root')).toBeInTheDocument();
    expect(screen.getByTestId('regrab-node-g_r1')).toBeInTheDocument();
    expect(screen.getByTestId('regrab-node-g_r2')).toBeInTheDocument();
  });

  it('triggers lazy ancestor fetch when an ancestor is missing', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'g_missing', series_title: 'X', season_number: 1,
          status: 'imported', coverage_count: 1,
          created_at: '2026-05-01T00:00:00Z',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    globalThis.fetch = fetchSpy as typeof fetch;
    const current = g({ id: 'g_cur', replay_of_id: 'g_missing', coverage_count: 2 });
    render(wrap(
      <ReGrabThread instance="alpha" grab={current} all={[current]} open={true} />,
    ));
    // The fetch should fire (`enabled` true, missing ancestor in chain).
    await new Promise((r) => setTimeout(r, 10));
    expect(fetchSpy).toHaveBeenCalled();
    expect((fetchSpy.mock.calls[0]?.[0] as string)).toMatch(/\/instances\/alpha\/grabs\/g_missing/);
  });

  it('does NOT trigger lazy fetch when closed', async () => {
    const fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as typeof fetch;
    const current = g({ id: 'g_cur', replay_of_id: 'g_missing' });
    render(wrap(
      <ReGrabThread instance="alpha" grab={current} all={[current]} open={false} />,
    ));
    await new Promise((r) => setTimeout(r, 10));
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
