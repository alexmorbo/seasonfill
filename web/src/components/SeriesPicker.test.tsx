import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { SeriesPicker } from './SeriesPicker';

const origFetch = globalThis.fetch;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

type Captured = { urls?: string[] };
function fetchStub(
  perPath: Record<string, (init?: RequestInit) => Response>,
  captured: Captured = {},
) {
  captured.urls ??= [];
  return vi.fn(async (url: RequestInfo | URL) => {
    const path = typeof url === 'string' ? url : url.toString();
    captured.urls!.push(path);
    for (const key of Object.keys(perPath)) {
      if (path.includes(key)) return perPath[key]!();
    }
    return json({ items: [], total: 0 });
  });
}

const FIXTURE = {
  items: [
    { series_id: 122, title: 'Severance', monitored: true, season_count: 2, missing_aired_count: 8 },
    { series_id: 99,  title: 'Andor',     monitored: true, season_count: 2, missing_aired_count: 3 },
    { series_id: 7,   title: 'Highlander', monitored: true, season_count: 1, missing_aired_count: 0 },
  ],
  total: 3,
};

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/scans', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('<SeriesPicker />', () => {
  it('renders a chip for each value with #id fallback when title is uncached', () => {
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[122, 99]} onChange={vi.fn()} />,
    );
    expect(screen.getByText('#122')).toBeInTheDocument();
    expect(screen.getByText('#99')).toBeInTheDocument();
  });

  it('typing triggers a debounced fetch (250 ms) and renders suggestions', async () => {
    // Real timers: fake-timer + RQ microtasks + userEvent interleaving
    // is unreliable. Wait the actual debounce window instead.
    const captured: Captured = {};
    globalThis.fetch = fetchStub(
      { 'instance=alpha': () => json(FIXTURE) }, captured,
    ) as typeof fetch;

    const user = userEvent.setup();
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[]} onChange={vi.fn()} />,
    );
    const input = screen.getByTestId('series-picker-input');
    await user.click(input);
    await user.type(input, 'sev');

    await waitFor(
      () => expect((captured.urls ?? []).some((u) => u.includes('q=sev'))).toBe(true),
      { timeout: 1000 },
    );
    expect(captured.urls!.at(-1)).toContain('q=sev');
    expect(captured.urls!.at(-1)).toContain('monitored=true');
    await waitFor(() => expect(screen.getByText('Severance')).toBeInTheDocument());
  });

  it('clicking a suggestion calls onChange and clears query', async () => {
    globalThis.fetch = fetchStub({
      'instance=alpha': () => json(FIXTURE),
    }) as typeof fetch;
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[]} onChange={onChange} />,
    );
    await user.click(screen.getByTestId('series-picker-input'));
    await screen.findByTestId('series-picker-opt-122');
    await user.click(screen.getByTestId('series-picker-opt-122'));
    expect(onChange).toHaveBeenCalledWith([122]);
    expect(
      (screen.getByTestId('series-picker-input') as HTMLInputElement).value,
    ).toBe('');
  });

  it('clicking the × on a chip removes that id', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[122, 99]} onChange={onChange} />,
    );
    await user.click(screen.getByRole('button', { name: /Remove #122/ }));
    expect(onChange).toHaveBeenCalledWith([99]);
  });

  it('ArrowDown + Enter picks the highlighted suggestion', async () => {
    globalThis.fetch = fetchStub({
      'instance=alpha': () => json(FIXTURE),
    }) as typeof fetch;
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[]} onChange={onChange} />,
    );
    await user.click(screen.getByTestId('series-picker-input'));
    await screen.findByTestId('series-picker-opt-122');
    await user.keyboard('{ArrowDown}');
    await user.keyboard('{Enter}');
    expect(onChange).toHaveBeenCalledWith([122]);
  });

  it('disabled — input + chip remove buttons are disabled', () => {
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[122]} onChange={vi.fn()} disabled />,
    );
    expect(screen.getByTestId('series-picker-input')).toBeDisabled();
    expect(screen.getByRole('button', { name: /Remove/ })).toBeDisabled();
  });

  it('shows spinner while in-flight and "No series match" when empty', async () => {
    // Two micro-cases combined: hung-fetch → spinner, then resolved
    // empty → empty-state copy.
    const holder: { resolve?: (r: Response) => void } = {};
    globalThis.fetch = vi.fn(
      () => new Promise<Response>((r) => { holder.resolve = r; }),
    ) as unknown as typeof fetch;
    const user = userEvent.setup();
    renderWithProviders(
      <SeriesPicker instance="alpha" value={[]} onChange={vi.fn()} />,
    );
    await user.click(screen.getByTestId('series-picker-input'));
    await user.type(screen.getByTestId('series-picker-input'), 'xyz');
    await waitFor(() =>
      expect(screen.getByTestId('series-picker-spinner')).toBeInTheDocument(),
    );
    holder.resolve?.(new Response('{"items":[],"total":0}', {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    expect(await screen.findByText(/no series match/i)).toBeInTheDocument();
  });
});
