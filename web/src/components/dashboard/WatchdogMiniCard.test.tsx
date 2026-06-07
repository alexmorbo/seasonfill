import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { WatchdogMiniCard } from './WatchdogMiniCard';

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const a = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...a, useNavigate: () => navigate };
});
vi.mock('@/lib/api/watchdogRollups', async () => {
  const a = await vi.importActual<typeof import('@/lib/api/watchdogRollups')>('@/lib/api/watchdogRollups');
  return { ...a, useWatchdogRollups: vi.fn() };
});
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
const useRoll = vi.mocked(useWatchdogRollups);

type RollupPatch = Partial<{ enabled: boolean; qbit_reachable: boolean; watched: number; regrabs_7d: number; blacklist_size: number; instance_name: string }>;
const r = (p: RollupPatch) => ({ instance_name: p.instance_name ?? 'h', enabled: p.enabled ?? false, active: !!p.enabled,
  watched: p.watched ?? 0, regrabs_7d: p.regrabs_7d ?? 0, blacklist_size: p.blacklist_size ?? 0, qbit_reachable: p.qbit_reachable ?? true });
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ok = <T,>(d: T) => ({ data: d, isPending: false, isError: false } as any);

describe('<WatchdogMiniCard />', () => {
  beforeEach(() => navigate.mockClear());

  it.each([
    ['running', [r({ enabled: true })]],
    ['unreachable', [r({ enabled: true }), r({ enabled: true, qbit_reachable: false })]],
    ['off', [r({ enabled: false })]],
  ])('chip resolves to %s', (expected, items) => {
    useRoll.mockReturnValue(ok({ items }));
    renderWithProviders(<WatchdogMiniCard />);
    expect(screen.getByTestId('watchdog-chip')).toHaveAttribute('data-chip', expected);
  });

  it('sums across instances; warn-colours blacklist when >0; navigates on open', () => {
    useRoll.mockReturnValue(ok({ items: [
      r({ instance_name: 'a', enabled: true, watched: 5, regrabs_7d: 1, blacklist_size: 0 }),
      r({ instance_name: 'b', enabled: true, watched: 7, regrabs_7d: 4, blacklist_size: 3 }),
    ] }));
    renderWithProviders(<WatchdogMiniCard />);
    expect(screen.getByTestId('wd-row-watched')).toHaveTextContent(/12/);
    expect(screen.getByTestId('wd-row-regrab7d')).toHaveTextContent('5');
    const bl = screen.getByTestId('wd-row-blacklist');
    expect(bl).toHaveTextContent('3');
    expect(bl.querySelector('b')?.className ?? '').toMatch(/text-warn/);
    fireEvent.click(screen.getByTestId('watchdog-open'));
    expect(navigate).toHaveBeenCalledWith('/watchdog');
  });
});
