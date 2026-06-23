import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { TMDBStatusBanner } from './TMDBStatusBanner';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('<TMDBStatusBanner />', () => {
  it('renders nothing when TMDB validation status is "valid"', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({
        services: [
          {
            service: 'tmdb',
            enabled: true,
            api_key_configured: true,
            api_key_masked: '****',
            proxy_url_set: false,
            proxy_auth_set: false,
            last_validation_status: 'valid',
          },
        ],
      }),
    );
    renderWithProviders(<TMDBStatusBanner />);
    // Give react-query a microtask tick before asserting absence.
    await waitFor(() => {
      expect(screen.queryByTestId('tmdb-status-banner')).not.toBeInTheDocument();
    });
  });

  it('renders warning banner when TMDB validation status is "invalid_key"', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({
        services: [
          {
            service: 'tmdb',
            enabled: true,
            api_key_configured: true,
            api_key_masked: '****',
            proxy_url_set: false,
            proxy_auth_set: false,
            last_validation_status: 'invalid_key',
            last_validation_message: '401 Invalid API key',
          },
        ],
      }),
    );
    renderWithProviders(<TMDBStatusBanner />);
    await waitFor(() => {
      expect(screen.getByTestId('tmdb-status-banner')).toBeInTheDocument();
    });
    expect(screen.getByTestId('tmdb-status-banner-link')).toHaveAttribute(
      'href',
      '/settings/external-services',
    );
  });

  it('renders nothing when TMDB row is missing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ services: [] }));
    renderWithProviders(<TMDBStatusBanner />);
    await waitFor(() => {
      expect(screen.queryByTestId('tmdb-status-banner')).not.toBeInTheDocument();
    });
  });
});
