// Story 522 / N-4e — verifies the button's visibility rule and that a
// click opens the modal (without exercising the modal's Select widgets).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { AddToSonarrButton } from './AddToSonarrButton';
import type { DiscoverySeriesItem } from '@/api/discovery';

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

function renderButton(itemOverrides: Partial<DiscoverySeriesItem> = {}) {
  const item: DiscoverySeriesItem = {
    series_id: 42, tmdb_id: 1399, tvdb_id: 81189,
    title: 'Rick and Morty', in_library_instances: [],
    ...itemOverrides,
  };
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={mkClient()}>
        <MemoryRouter>
          <AddToSonarrButton item={item} />
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
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/discover', assign: vi.fn() },
  });
});

afterEach(() => { globalThis.fetch = origFetch; });

describe('<AddToSonarrButton />', () => {
  it('renders when in_library_instances is empty', () => {
    renderButton();
    expect(screen.getByTestId('add-to-sonarr-button')).toBeInTheDocument();
  });

  it('renders when in_library_instances is omitted', () => {
    // Cast away the omitted-optional ambiguity: the production type
    // accepts `undefined` via optionality, but our `Partial<>` helper
    // rejects it under exactOptionalPropertyTypes.
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={mkClient()}>
          <MemoryRouter>
            <AddToSonarrButton
              item={{
                series_id: 1, tmdb_id: 1, tvdb_id: 1, title: 'X',
              } as DiscoverySeriesItem}
            />
          </MemoryRouter>
        </QueryClientProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('add-to-sonarr-button')).toBeInTheDocument();
  });

  it('renders nothing when the series is already in a library', () => {
    renderButton({ in_library_instances: ['sonarr-main'] });
    expect(screen.queryByTestId('add-to-sonarr-button')).toBeNull();
  });

  it('opens the modal on click', () => {
    renderButton();
    fireEvent.click(screen.getByTestId('add-to-sonarr-button'));
    expect(screen.getByTestId('add-to-sonarr-modal')).toBeInTheDocument();
  });
});
