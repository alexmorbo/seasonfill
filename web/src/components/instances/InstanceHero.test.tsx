import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceHero } from './InstanceHero';

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
    message: (m: string) => toastMessage(m),
  },
}));

const origFetch = globalThis.fetch;

beforeEach(() => {
  // Mock every dependent endpoint. Sparkline data lives at counters?window=7d.
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/counters?window=24h')) {
      return new Response(JSON.stringify({
        instance_name: 'homelab', window: '24h',
        totals: { grabs: 12, imports: 8, fails: 0 },
        sparkline: [], avg_grabs_7d: 6.7,
      }), { status: 200 });
    }
    if (url.endsWith('/counters?window=7d')) {
      return new Response(JSON.stringify({
        instance_name: 'homelab', window: '7d',
        totals: { grabs: 47, imports: 39, fails: 2 },
        sparkline: [
          { date: '2026-06-01', grabs: 4, imports: 3, fails: 0 },
          { date: '2026-06-02', grabs: 6, imports: 5, fails: 0 },
          { date: '2026-06-03', grabs: 2, imports: 1, fails: 0 },
          { date: '2026-06-04', grabs: 9, imports: 8, fails: 1 },
          { date: '2026-06-05', grabs: 4, imports: 4, fails: 0 },
          { date: '2026-06-06', grabs: 6, imports: 6, fails: 0 },
          { date: '2026-06-07', grabs: 7, imports: 7, fails: 0 },
        ],
        avg_grabs_7d: 6.7,
      }), { status: 200 });
    }
    if (url.endsWith('/missing')) {
      return new Response(JSON.stringify({ items: Array.from({ length: 294 }, () => ({})) }), { status: 200 });
    }
    if (url.endsWith('/webhook/status')) {
      return new Response(JSON.stringify({ installed: true }), { status: 200 });
    }
    if (url.endsWith('/qbit/settings')) {
      return new Response(JSON.stringify({ enabled: true }), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  }) as never;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<InstanceHero />', () => {
  const inst = {
    name: 'homelab',
    mode: 'auto',
    health: 'Available',
    last_check_at: new Date().toISOString(),
    transitions_count: 0,
    url: 'http://sonarr:80',
  } as never;

  it('renders 24h + 7d stats, sparkline, and chip row', async () => {
    renderWithProviders(
      <InstanceHero
        instance={inst}
        onEdit={() => undefined}
      />,
    );
    await waitFor(() => {
      expect(screen.getByTestId('hero-sparkline')).toBeInTheDocument();
    });
    // 24h + 7d stats blocks (should appear after data loads)
    await waitFor(() => {
      expect(screen.getAllByTestId('instance-stats-block').length).toBe(2);
    });
    // Chip row
    expect(await screen.findByTestId('chip-missing')).toHaveTextContent(/294/);
    expect(await screen.findByTestId('chip-watchdog')).toHaveTextContent(/running/i);
    const webhookChip = await screen.findByTestId('chip-webhook');
    expect(webhookChip.className).toMatch(/ok/);
  });

  it('applies danger left-border + last-error when degraded', async () => {
    const degraded = {
      name: 'homelab',
      mode: 'auto',
      health: 'Unreachable',
      last_check_at: new Date().toISOString(),
      transitions_count: 0,
      url: 'http://sonarr:80',
      last_error: 'dial tcp — connection refused',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={degraded}
        onEdit={() => undefined}
      />,
    );
    const card = screen.getByTestId('instance-hero-homelab');
    expect(card.className).toMatch(/border-l-status-danger/);
    const errorEl = await screen.findByTestId('hero-error');
    expect(errorEl).toHaveTextContent(/connection refused/);
  });

  it('"Sonarr" link prefers public_url over url', async () => {
    const withPublic = {
      ...(inst as object),
      url: 'http://sonarr:80',
      public_url: 'https://sonarr.example.com',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={withPublic}
        onEdit={() => undefined}
      />,
    );
    const link = await screen.findByTestId('hero-sonarr-link-homelab');
    expect(link).toHaveAttribute('href', 'https://sonarr.example.com');
  });

  it('"Sonarr" link falls back to url when public_url is empty', async () => {
    const noPublic = {
      ...(inst as object),
      url: 'http://sonarr:80',
      public_url: '',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={noPublic}
        onEdit={() => undefined}
      />,
    );
    const link = await screen.findByTestId('hero-sonarr-link-homelab');
    expect(link).toHaveAttribute('href', 'http://sonarr:80');
  });

  it('"Sonarr" link falls back to url when public_url key is OMITTED (omitempty JSON shape)', async () => {
    // Reproduces the operator-reported state in finding N-2:
    //   URL field = `http://sonarr:80`, Public URL field blank
    // → backend serialises `dto.Instance` with `public_url` OMITTED
    // (json:"public_url,omitempty"), so the SPA sees `public_url ===
    // undefined`. The hero link must navigate to the internal URL.
    const omitted = {
      name: 'homelab',
      mode: 'auto',
      health: 'Available',
      last_check_at: new Date().toISOString(),
      transitions_count: 0,
      url: 'http://sonarr:80',
      // public_url key intentionally absent
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={omitted}
        onEdit={() => undefined}
      />,
    );
    const link = await screen.findByTestId('hero-sonarr-link-homelab');
    expect(link).toHaveAttribute('href', 'http://sonarr:80');
  });

  it('SelfThrottled wears the amber warning accent, not the red danger accent', async () => {
    const throttled = {
      name: 'homelab',
      mode: 'auto',
      health: 'SelfThrottled',
      last_check_at: new Date().toISOString(),
      transitions_count: 0,
      url: 'http://sonarr:80',
      last_error: 'global rate limit wait /api/v3/system/status: context deadline exceeded',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={throttled}
        onEdit={() => undefined}
      />,
    );
    const card = screen.getByTestId('instance-hero-homelab');
    // Warning border — NOT the red danger border the operator complained about.
    expect(card.className).toMatch(/border-l-status-warning/);
    expect(card.className).not.toMatch(/border-l-status-danger/);
    const pill = screen.getByTestId('hero-health-homelab');
    expect(pill.textContent).toMatch(/Throttled/);
    const errorEl = await screen.findByTestId('hero-error');
    expect(errorEl.className).toMatch(/text-status-warning/);
  });

  it('renders spinner pill + neutral kind + "Checking connection…" label when health is Bootstrapping', async () => {
    const bootstrapping = {
      name: 'homelab',
      mode: 'auto',
      health: 'Bootstrapping',
      last_check_at: new Date().toISOString(),
      transitions_count: 0,
      url: 'http://sonarr:80',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={bootstrapping}
        onEdit={() => undefined}
      />,
    );
    expect(screen.getByTestId('hero-health-spinner-homelab')).toBeInTheDocument();
    const pill = screen.getByTestId('hero-health-homelab');
    expect(pill.textContent).toMatch(/Checking connection|Проверяем подключение/);
    // Bootstrapping is neutral, not danger — operator must NOT see a red accent.
    const card = screen.getByTestId('instance-hero-homelab');
    expect(card.className).not.toMatch(/border-l-status-danger/);
    expect(card.className).not.toMatch(/border-l-status-warning/);
  });

  it('"Sonarr" button is hidden when url is schemeless and no public_url', async () => {
    const bare = {
      ...(inst as object),
      url: 'sonarr',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={bare}
        onEdit={() => undefined}
      />,
    );
    // Wait for a stable render via an unrelated chip query.
    await screen.findByTestId('chip-missing');
    expect(screen.queryByTestId('hero-sonarr-link-homelab')).toBeNull();
  });
});

describe('<InstanceHero /> — Force scan button busy/running UX', () => {
  const inst = {
    name: 'homelab',
    mode: 'auto',
    health: 'Available',
    last_check_at: new Date().toISOString(),
    transitions_count: 0,
    url: 'http://sonarr:80',
  } as never;

  beforeEach(() => {
    toastSuccess.mockClear();
    toastError.mockClear();
    toastMessage.mockClear();
  });

  it('starts in idle state when no scan is running for the instance', async () => {
    // Default fetch mock returns `{}` for /scans → no running scan
    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    const btn = await screen.findByTestId('hero-force-scan-homelab');
    expect(btn).toHaveAttribute('data-busy', 'false');
    expect(btn).not.toBeDisabled();
    expect(btn.textContent).toMatch(/Force scan/);
  });

  it('starts in running state when /scans says latest run is running (page reload during in-flight scan)', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/scans?instance=homelab')) {
        return new Response(JSON.stringify({
          items: [{
            id: 'run-1', instance: 'homelab', status: 'running',
            started_at: new Date().toISOString(), trigger: 'manual',
          }],
        }), { status: 200 });
      }
      if (url.includes('/counters')) {
        return new Response(JSON.stringify({
          instance_name: 'homelab', window: url.includes('24h') ? '24h' : '7d',
          totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }) as never;

    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    await waitFor(() => {
      const btn = screen.getByTestId('hero-force-scan-homelab');
      expect(btn).toHaveAttribute('data-busy', 'true');
    });
    const btn = screen.getByTestId('hero-force-scan-homelab');
    expect(btn).toBeDisabled();
    expect(btn.textContent).toMatch(/Scanning|Сканирование/);
  });

  it('clicking transitions to running state + fires Scan started toast', async () => {
    // First /scans poll → no run; POST /scan → 202 running; subsequent /scans → running
    let scanRunning = false;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scan') && init?.method === 'POST') {
        scanRunning = true;
        return new Response(JSON.stringify([{
          scan_run_id: 'run-7', instance: 'homelab', status: 'running',
          started_at: new Date().toISOString(),
        }]), { status: 202, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.includes('/scans?instance=homelab')) {
        const items = scanRunning ? [{
          id: 'run-7', instance: 'homelab', status: 'running',
          started_at: new Date().toISOString(), trigger: 'manual',
        }] : [];
        return new Response(JSON.stringify({ items }), { status: 200 });
      }
      if (url.includes('/counters')) {
        return new Response(JSON.stringify({
          instance_name: 'homelab', window: url.includes('24h') ? '24h' : '7d',
          totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }) as never;

    const user = userEvent.setup();
    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    const btn = await screen.findByTestId('hero-force-scan-homelab');
    expect(btn).not.toBeDisabled();

    await user.click(btn);

    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringMatching(/Scan started for homelab|Сканирование запущено/),
      );
    });
    await waitFor(() => {
      expect(screen.getByTestId('hero-force-scan-homelab')).toHaveAttribute('data-busy', 'true');
    });
  });

  it('409 surfaces "already running" toast and clamps the button busy', async () => {
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scan') && init?.method === 'POST') {
        return new Response(JSON.stringify({
          error: 'scan already running', instance: 'homelab', code: 'SCAN_IN_PROGRESS',
        }), { status: 409, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.includes('/scans?instance=homelab')) {
        // Empty initially so the button is clickable on first render
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      if (url.includes('/counters')) {
        return new Response(JSON.stringify({
          instance_name: 'homelab', window: url.includes('24h') ? '24h' : '7d',
          totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }) as never;

    const user = userEvent.setup();
    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    const btn = await screen.findByTestId('hero-force-scan-homelab');
    await user.click(btn);

    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith(
        expect.stringMatching(/already running|уже идёт/i),
      );
    });
  });

  it('rapid clicks while busy do not pile up additional POSTs (button no-ops)', async () => {
    const fetchSpy = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/scans?instance=homelab')) {
        return new Response(JSON.stringify({
          items: [{
            id: 'run-1', instance: 'homelab', status: 'running',
            started_at: new Date().toISOString(), trigger: 'manual',
          }],
        }), { status: 200 });
      }
      if (url.includes('/counters')) {
        return new Response(JSON.stringify({
          instance_name: 'homelab', window: url.includes('24h') ? '24h' : '7d',
          totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    });
    globalThis.fetch = fetchSpy as never;

    const user = userEvent.setup();
    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    await waitFor(() => {
      expect(screen.getByTestId('hero-force-scan-homelab')).toHaveAttribute('data-busy', 'true');
    });
    const btn = screen.getByTestId('hero-force-scan-homelab');
    // Three rapid clicks; button is disabled, none of them should hit POST /scan
    await user.click(btn);
    await user.click(btn);
    await user.click(btn);

    const postCalls = fetchSpy.mock.calls.filter(([u, init]) => {
      const url = String(u);
      const method = (init as RequestInit | undefined)?.method;
      return url.endsWith('/scan') && method === 'POST';
    });
    expect(postCalls.length).toBe(0);
  });

  it('click → running → completed flow fires "started" then "finished" toasts and re-enables button', { timeout: 15_000 }, async () => {
    // Click → POST 202 → invalidation triggers /scans refetch → status
    // 'running' → button stays busy. Next poll → 'completed' → hook
    // detects the transition, fires the finished toast, and the button
    // returns to the idle state. We use fake timers to skip the 6 s poll.
    let phase: 'idle' | 'running' | 'completed' = 'idle';
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/scan') && init?.method === 'POST') {
        phase = 'running';
        return new Response(JSON.stringify([{
          scan_run_id: 'run-x', instance: 'homelab', status: 'running',
          started_at: new Date().toISOString(),
        }]), { status: 202, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.includes('/scans?instance=homelab')) {
        if (phase === 'idle') {
          return new Response(JSON.stringify({ items: [] }), { status: 200 });
        }
        const status = phase;
        return new Response(JSON.stringify({
          items: [{
            id: 'run-x', instance: 'homelab', status,
            started_at: new Date().toISOString(),
            finished_at: status === 'completed' ? new Date().toISOString() : undefined,
            trigger: 'manual',
          }],
        }), { status: 200 });
      }
      if (url.includes('/counters')) {
        return new Response(JSON.stringify({
          instance_name: 'homelab', window: url.includes('24h') ? '24h' : '7d',
          totals: { grabs: 0, imports: 0, fails: 0 }, sparkline: [], avg_grabs_7d: 0,
        }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    }) as never;

    const user = userEvent.setup();
    renderWithProviders(<InstanceHero instance={inst} onEdit={() => undefined} />);
    const btn = await screen.findByTestId('hero-force-scan-homelab');
    await user.click(btn);

    // After click: started toast fires; button transitions to busy as the
    // invalidation-triggered refetch reads `running`.
    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringMatching(/Scan started for homelab|Сканирование запущено/),
      );
    });
    await waitFor(() => {
      expect(screen.getByTestId('hero-force-scan-homelab')).toHaveAttribute('data-busy', 'true');
    });

    // Advance the test "phase" to completed and force a refetch (mimics
    // the next 6 s poll tick). We poke fetch via dispatching a window
    // 'focus' wouldn't help (refetchOnWindowFocus is off), so just wait
    // — the 6 s interval will tick. Bump the test timeout via waitFor.
    phase = 'completed';
    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringMatching(/Scan finished|Сканирование завершено/),
      );
    }, { timeout: 8000 });
    await waitFor(() => {
      expect(screen.getByTestId('hero-force-scan-homelab')).toHaveAttribute('data-busy', 'false');
    });
  });
});
