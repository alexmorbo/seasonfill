import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { AlertsCard } from './AlertsCard';

vi.mock('@/lib/api/webhookStatus', () => ({ useWebhookStatusAggregate: vi.fn() }));
vi.mock('@/lib/instances', () => ({ useInstances: vi.fn() }));
import { useWebhookStatusAggregate } from '@/lib/api/webhookStatus';
import { useInstances } from '@/lib/instances';
const useWh = vi.mocked(useWebhookStatusAggregate);
const useInst = vi.mocked(useInstances);

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ok = <T,>(d: T) => ({ data: d, isPending: false, isError: false } as any);
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const err = () => ({ data: undefined, isPending: false, isError: true } as any);

describe('<AlertsCard />', () => {
  beforeEach(() => { useWh.mockReset(); useInst.mockReset(); });

  it('renders danger row before warn row + count badge', () => {
    useWh.mockReturnValue(ok({ items: [{ instance_name: 'h', installed: true, healthy: false, error: 'sonarr 503' }], healthy_count: 0, unhealthy_count: 1 }));
    useInst.mockReturnValue(ok({ instances: [{ name: '4k', health: 'unavailable', last_error: 'refused' }] }));
    renderWithProviders(<AlertsCard />);
    const rows = screen.getAllByTestId(/^alert-row-/);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveAttribute('data-severity', 'danger');
    expect(rows[1]).toHaveAttribute('data-severity', 'warn');
    expect(screen.getByTestId('alerts-count')).toHaveTextContent('2');
  });

  it('renders allclear when no alerts + filters healthy sources', () => {
    useWh.mockReturnValue(ok({ items: [{ instance_name: 'a', installed: true, healthy: true }], healthy_count: 1, unhealthy_count: 0 }));
    useInst.mockReturnValue(ok({ instances: [{ name: 'a', health: 'available' }] }));
    renderWithProviders(<AlertsCard />);
    expect(screen.getByTestId('alerts-allclear')).toBeInTheDocument();
    expect(screen.queryAllByTestId(/^alert-row-/)).toHaveLength(0);
  });

  it('degrades silently on partial fetch failure', () => {
    useWh.mockReturnValue(err());
    useInst.mockReturnValue(ok({ instances: [{ name: 'x', health: 'unavailable', last_error: 'down' }] }));
    renderWithProviders(<AlertsCard />);
    expect(screen.getAllByTestId(/^alert-row-/)).toHaveLength(1);
    expect(screen.queryByTestId('alerts-load-failed')).toBeNull();
  });

  it('shows warn glyph only when BOTH sources fail', () => {
    useWh.mockReturnValue(err()); useInst.mockReturnValue(err());
    renderWithProviders(<AlertsCard />);
    expect(screen.getByTestId('alerts-load-failed')).toBeInTheDocument();
  });
});
