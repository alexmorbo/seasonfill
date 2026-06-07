import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';

const navigate = vi.fn();
const mutate = vi.fn();

vi.mock('react-router-dom', async () => {
  const a = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...a, useNavigate: () => navigate };
});
vi.mock('@/lib/scan-mutations', () => ({ useTriggerScan: () => ({ mutate, isPending: false }) }));
vi.mock('@/lib/instances', () => ({ useInstances: () => ({ data: { instances: [{ name: 'homelab' }] } }) }));
vi.mock('@/lib/instance-filter-context-internal', () => ({ useInstanceFilter: () => ({ filter: null, setFilter: vi.fn() }) }));
vi.mock('sonner', () => {
  const toast = { success: vi.fn(), error: vi.fn(), message: vi.fn() };
  return { toast };
});

import { QuickActionsCard } from './QuickActionsCard';
import { toast } from 'sonner';

describe('<QuickActionsCard />', () => {
  beforeEach(() => { navigate.mockClear(); mutate.mockClear(); vi.mocked(toast.success).mockClear(); vi.mocked(toast.error).mockClear(); });

  it('scan-all → toast success + navigate /scans', async () => {
    mutate.mockImplementation((_b, o) => o.onSuccess?.([{ scan_run_id: 'r1' }, { scan_run_id: 'r2' }]));
    renderWithProviders(<QuickActionsCard />);
    fireEvent.click(screen.getByTestId('qa-scan-all'));
    expect(mutate).toHaveBeenCalledWith({}, expect.any(Object));
    await waitFor(() => { expect(vi.mocked(toast.success)).toHaveBeenCalled(); expect(navigate).toHaveBeenCalledWith('/scans'); });
  });

  it('scan-all error → toast error, no navigation', async () => {
    mutate.mockImplementation((_b, o) => o.onError?.(Object.assign(new Error('500'), { status: 500, message: '500' })));
    renderWithProviders(<QuickActionsCard />);
    fireEvent.click(screen.getByTestId('qa-scan-all'));
    await waitFor(() => { expect(vi.mocked(toast.error)).toHaveBeenCalled(); expect(navigate).not.toHaveBeenCalled(); });
  });

  it('last-fail + queue buttons navigate correctly', () => {
    renderWithProviders(<QuickActionsCard />);
    fireEvent.click(screen.getByTestId('qa-last-fail'));
    expect(navigate).toHaveBeenCalledWith('/grabs?status=import_failed');
    fireEvent.click(screen.getByTestId('qa-queue'));
    expect(navigate).toHaveBeenCalledWith('/instances/homelab/queue');
  });
});
