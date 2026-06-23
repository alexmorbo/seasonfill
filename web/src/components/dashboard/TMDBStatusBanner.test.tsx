import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
  window.localStorage.removeItem('tmdb_disabled_banner_dismissed');
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
    expect(screen.getByTestId('tmdb-status-banner')).toHaveAttribute('data-variant', 'invalid_key');
    expect(screen.getByTestId('tmdb-status-banner-link')).toHaveAttribute(
      'href',
      '/settings/external-services',
    );
  });

  it('B-16b: renders missing banner when TMDB row is absent', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ services: [] }));
    renderWithProviders(<TMDBStatusBanner />);
    await waitFor(() => {
      const banner = screen.getByTestId('tmdb-status-banner');
      expect(banner).toHaveAttribute('data-variant', 'missing');
    });
    expect(screen.getByTestId('tmdb-status-banner-link')).toHaveAttribute(
      'href',
      '/settings/external-services',
    );
  });

  it('B-16b: dismiss persists in localStorage and hides the banner', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ services: [] }));
    renderWithProviders(<TMDBStatusBanner />);
    const dismissBtn = await screen.findByTestId('tmdb-status-banner-dismiss');
    await userEvent.click(dismissBtn);
    expect(window.localStorage.getItem('tmdb_disabled_banner_dismissed')).toBe('1');
    await waitFor(() => {
      expect(screen.queryByTestId('tmdb-status-banner')).not.toBeInTheDocument();
    });
  });

  it('B-16b: invalid_key (warn) preempts missing (info) even when dismissed', async () => {
    window.localStorage.setItem('tmdb_disabled_banner_dismissed', '1');
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
          },
        ],
      }),
    );
    renderWithProviders(<TMDBStatusBanner />);
    await waitFor(() => {
      const banner = screen.getByTestId('tmdb-status-banner');
      expect(banner).toHaveAttribute('data-variant', 'invalid_key');
    });
  });

  it('B-16b: row present but enabled=false + api_key_configured=false → missing variant', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({
        services: [
          {
            service: 'tmdb',
            enabled: false,
            api_key_configured: false,
            api_key_masked: '',
            proxy_url_set: false,
            proxy_auth_set: false,
          },
        ],
      }),
    );
    renderWithProviders(<TMDBStatusBanner />);
    await waitFor(() => {
      expect(screen.getByTestId('tmdb-status-banner')).toHaveAttribute('data-variant', 'missing');
    });
  });
});
