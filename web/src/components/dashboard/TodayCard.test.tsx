import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { TodayCard, classifyTrend } from './TodayCard';

vi.mock('@/lib/api/counters', async () => {
  const a = await vi.importActual<typeof import('@/lib/api/counters')>('@/lib/api/counters');
  return { ...a, useCountersAggregate: vi.fn() };
});
import { useCountersAggregate } from '@/lib/api/counters';
const useC = vi.mocked(useCountersAggregate);

const day = { items: [{ instance_name: 'h', window: '24h' as const,
  totals: { grabs: 12, imports: 11, fails: 1 }, sparkline: [], avg_grabs_7d: 8 }] };
const week = { items: [{ instance_name: 'h', window: '7d' as const,
  totals: { grabs: 56, imports: 51, fails: 5 },
  sparkline: [3, 6, 2, 8, 4, 6, 12].map((g, i) => ({ date: `2026-06-0${i + 1}T00:00:00Z`, grabs: g, imports: g, fails: 0 })),
  avg_grabs_7d: 8 }] };

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ok = <T,>(d: T) => ({ data: d, isPending: false, isError: false } as any);
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const err = () => ({ data: undefined, isPending: false, isError: true } as any);

describe('classifyTrend', () => {
  it('classifies up/down/flat', () => {
    expect(classifyTrend(12, 8)).toBe('up');
    expect(classifyTrend(2, 8)).toBe('down');
    expect(classifyTrend(8, 8)).toBe('flat');
    expect(classifyTrend(0, 0)).toBe('flat');
    expect(classifyTrend(0, 0.4)).toBe('flat');
  });
});

describe('<TodayCard />', () => {
  beforeEach(() => useC.mockReset());

  it('renders big number, density bar, 7-bar sparkline with one peak, and trend chip', () => {
    useC.mockImplementation((w) => (w === '24h' ? ok(day) : ok(week)));
    renderWithProviders(<TodayCard />);
    expect(screen.getByTestId('today-big-n')).toHaveTextContent('12');
    expect(screen.getByTestId('density-bar')).toBeInTheDocument();
    const spark = screen.getByRole('img', { name: /sparkline|7-day|Sparkline/i });
    expect(spark.querySelectorAll('span[data-peak]').length).toBe(7);
    expect(spark.querySelectorAll('span[data-peak="true"]').length).toBe(1);
    expect(screen.getByTestId('trend-chip')).toHaveAttribute('data-trend', 'up');
  });

  it('shows em-dash + warn icon when 24h fetch errors', () => {
    useC.mockImplementation((w) => (w === '24h' ? err() : ok(week)));
    renderWithProviders(<TodayCard />);
    expect(screen.getByTestId('today-big-n')).toHaveTextContent('—');
    expect(screen.getByTestId('today-load-failed')).toBeInTheDocument();
  });
});
