import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CancelScanDialog } from './CancelScanDialog';

const toastSuccess = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: { success: (m: string) => toastSuccess(m),
    error: vi.fn(), message: (m: string) => toastMessage(m) },
}));

const origFetch = globalThis.fetch;
const renderD = (scanId = 'abc-123') => {
  const qc = new QueryClient({ defaultOptions: {
    queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={qc}><CancelScanDialog scanId={scanId} /></QueryClientProvider>);
};

beforeEach(() => {
  toastSuccess.mockClear();
  toastMessage.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/scans/abc', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<CancelScanDialog />', () => {
  it('renders trigger button, opens dialog on click', async () => {
    renderD();
    expect(screen.queryByTestId('cancel-scan-dialog')).not.toBeInTheDocument();
    await userEvent.click(screen.getByTestId('cancel-scan-button'));
    expect(await screen.findByTestId('cancel-scan-dialog')).toBeInTheDocument();
    expect(screen.getByText(/Already-issued grabs are NOT undone/i)).toBeInTheDocument();
  });

  it('confirming POSTs /scans/:id/cancel and closes the dialog', async () => {
    const captured: { url?: string; method?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (init?.method) captured.method = init.method;
      return new Response(JSON.stringify({ ok: true }),
        { status: 202, headers: { 'Content-Type': 'application/json' } });
    }) as typeof fetch;

    renderD();
    await userEvent.click(screen.getByTestId('cancel-scan-button'));
    await userEvent.click(await screen.findByTestId('cancel-scan-confirm'));
    await waitFor(() => expect(captured.url).toBe('/api/v1/scans/abc-123/cancel'));
    expect(captured.method).toBe('POST');
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Scan cancellation requested'));
    await waitFor(() => expect(screen.queryByTestId('cancel-scan-dialog')).not.toBeInTheDocument());
  });

  it('"Keep running" closes the dialog without dispatching a request', async () => {
    const fetchSpy = vi.fn(async () => new Response('{}')) as unknown as typeof fetch;
    globalThis.fetch = fetchSpy;
    renderD();
    await userEvent.click(screen.getByTestId('cancel-scan-button'));
    await userEvent.click(await screen.findByRole('button', { name: /keep running/i }));
    await waitFor(() => expect(screen.queryByTestId('cancel-scan-dialog')).not.toBeInTheDocument());
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
