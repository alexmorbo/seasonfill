import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceHero } from './InstanceHero';

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
        onForceScan={() => undefined}
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
        onForceScan={() => undefined}
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
        onForceScan={() => undefined}
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
        onForceScan={() => undefined}
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
        onForceScan={() => undefined}
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
        onForceScan={() => undefined}
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

  it('"Sonarr" button is hidden when url is schemeless and no public_url', async () => {
    const bare = {
      ...(inst as object),
      url: 'sonarr',
    } as never;
    renderWithProviders(
      <InstanceHero
        instance={bare}
        onEdit={() => undefined}
        onForceScan={() => undefined}
      />,
    );
    // Wait for a stable render via an unrelated chip query.
    await screen.findByTestId('chip-missing');
    expect(screen.queryByTestId('hero-sonarr-link-homelab')).toBeNull();
  });
});
