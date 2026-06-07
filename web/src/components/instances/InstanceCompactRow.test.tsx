import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceCompactRow } from './InstanceCompactRow';

const origFetch = globalThis.fetch;

beforeEach(() => {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/counters?window=7d')) {
      return new Response(JSON.stringify({
        instance_name: '4k', window: '7d',
        totals: { grabs: 0, imports: 0, fails: 0 },
        sparkline: [], avg_grabs_7d: 0,
      }), { status: 200 });
    }
    return new Response('{}', { status: 200 });
  }) as never;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<InstanceCompactRow />', () => {
  it('applies degraded tint + error line + flips chip', async () => {
    renderWithProviders(
      <InstanceCompactRow
        instance={{
          name: '4k',
          mode: 'manual',
          health: 'Unreachable',
          last_check_at: new Date().toISOString(),
          transitions_count: 3,
          url: 'http://sonarr-4k:80',
          last_error: 'dial tcp — connection refused',
        } as never}
        onEdit={() => undefined}
        onRecheck={() => undefined}
        onDelete={() => undefined}
      />,
    );
    const row = screen.getByTestId('instance-row-4k');
    expect(row.className).toMatch(/border-l-status-danger/);
    expect(screen.getByTestId('row-error-4k')).toHaveTextContent(/connection refused/);
    expect(screen.getByTestId('row-flips-4k')).toHaveTextContent('3');
    await waitFor(() => {
      expect(screen.getByTestId('row-counts-4k')).toHaveTextContent('0 / 0 / 0');
    });
  });

  it('hides flips chip and error line when healthy', () => {
    renderWithProviders(
      <InstanceCompactRow
        instance={{
          name: 'beta', mode: 'auto', health: 'Available',
          last_check_at: new Date().toISOString(), transitions_count: 0,
          url: 'http://beta:80',
        } as never}
        onEdit={() => undefined}
        onRecheck={() => undefined}
        onDelete={() => undefined}
      />,
    );
    expect(screen.queryByTestId('row-flips-beta')).toBeNull();
    expect(screen.queryByTestId('row-error-beta')).toBeNull();
  });
});
