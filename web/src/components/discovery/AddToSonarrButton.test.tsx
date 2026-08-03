// S5 / ADR-0008 — the button is now a surface-agnostic trigger that calls
// openAddToSonarr(target) from AddToSonarrProvider. In-library gating lives in
// SeriesCard now, so this suite only covers the trigger + provider wiring.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { AddToSonarrButton } from './AddToSonarrButton';
import { AddToSonarrProvider } from './AddToSonarrProvider';
import type { AddToSonarrTarget } from './add-to-sonarr-context';

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

const origFetch = globalThis.fetch;

function mkClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

const TARGET: AddToSonarrTarget = { title: 'Rick and Morty', tvdbId: 81189 };

function renderButton(target: AddToSonarrTarget = TARGET) {
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={mkClient()}>
        <MemoryRouter>
          <AddToSonarrProvider>
            <AddToSonarrButton target={target} />
          </AddToSonarrProvider>
        </MemoryRouter>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

beforeEach(() => {
  globalThis.fetch = vi.fn(async () =>
    new Response('{}', { status: 200,
      headers: { 'Content-Type': 'application/json' } }),
  ) as typeof fetch;
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<AddToSonarrButton />', () => {
  it('renders the trigger', () => {
    renderButton();
    expect(screen.getByTestId('add-to-sonarr-button')).toBeInTheDocument();
  });

  it('opens the provider modal on click', () => {
    renderButton();
    fireEvent.click(screen.getByTestId('add-to-sonarr-button'));
    expect(screen.getByTestId('add-to-sonarr-modal')).toBeInTheDocument();
  });

  it('throws a clear error if used without the provider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() =>
      render(
        <I18nextProvider i18n={i18n}>
          <MemoryRouter>
            <AddToSonarrButton target={TARGET} />
          </MemoryRouter>
        </I18nextProvider>,
      ),
    ).toThrow(/AddToSonarrProvider/);
    spy.mockRestore();
  });
});
