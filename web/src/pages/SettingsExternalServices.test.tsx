import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, fireEvent, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { SettingsExternalServices } from './SettingsExternalServices';

interface MockedExtSvc {
  service: string;
  enabled: boolean;
  api_key_masked: string;
  api_key_configured: boolean;
  proxy_url_set: boolean;
  proxy_auth_set: boolean;
  last_validation_status?: 'valid' | 'invalid_key';
  last_validation_message?: string;
}

function jsonResponse(body: unknown, status = 200, headers?: Record<string, string>): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...(headers ?? {}) },
  });
}

function makeServices(overrides: Partial<MockedExtSvc> = {}): MockedExtSvc[] {
  return [
    {
      service: 'tmdb',
      enabled: true,
      api_key_masked: '****abcd',
      api_key_configured: true,
      proxy_url_set: false,
      proxy_auth_set: false,
      ...overrides,
    },
    {
      service: 'omdb',
      enabled: false,
      api_key_masked: '',
      api_key_configured: false,
      proxy_url_set: false,
      proxy_auth_set: false,
    },
    {
      service: 'tvdb',
      enabled: false,
      api_key_masked: '',
      api_key_configured: false,
      proxy_url_set: false,
      proxy_auth_set: false,
    },
  ];
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('<SettingsExternalServices />', () => {
  it('renders "401 Invalid Key" badge on TMDB card when last_validation_status === "invalid_key"', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/external-services')) {
        return jsonResponse({
          services: makeServices({
            last_validation_status: 'invalid_key',
            last_validation_message: '401 Invalid API key',
          }),
        });
      }
      return jsonResponse({}, 404);
    });
    renderWithProviders(<SettingsExternalServices />);
    await waitFor(() => {
      expect(screen.getByTestId('validation-badge-tmdb')).toBeInTheDocument();
    });
    // OMDb and TVDB rows must NOT get the badge — they don't carry the flag.
    expect(screen.queryByTestId('validation-badge-omdb')).not.toBeInTheDocument();
    expect(screen.queryByTestId('validation-badge-tvdb')).not.toBeInTheDocument();
  });

  it('hides the badge when last_validation_status === "valid"', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/external-services')) {
        return jsonResponse({
          services: makeServices({ last_validation_status: 'valid' }),
        });
      }
      return jsonResponse({}, 404);
    });
    renderWithProviders(<SettingsExternalServices />);
    await waitFor(() => {
      expect(screen.getByTestId('ext-card-tmdb')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('validation-badge-tmdb')).not.toBeInTheDocument();
  });

  it('renders inline error under TMDB API Key input on 422 external_service_invalid_key', async () => {
    let putCount = 0;
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input);
      const method = (init?.method ?? 'GET').toUpperCase();
      if (method === 'PUT' && url.endsWith('/external-services/tmdb')) {
        putCount++;
        return jsonResponse(
          {
            error: 'external_service_invalid_key',
            message: 'TMDB rejected the key',
          },
          422,
        );
      }
      if (url.endsWith('/external-services')) {
        return jsonResponse({ services: makeServices() });
      }
      return jsonResponse({}, 404);
    });
    renderWithProviders(<SettingsExternalServices />);
    await waitFor(() => {
      expect(screen.getByTestId('ext-card-tmdb')).toBeInTheDocument();
    });
    // Type a new key and save.
    const input = screen.getByTestId('ext-api-key-tmdb') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'bad-key' } });
    fireEvent.click(screen.getByTestId('ext-save-tmdb'));
    await waitFor(() => {
      expect(screen.getByTestId('ext-api-key-error-tmdb')).toBeInTheDocument();
    });
    expect(putCount).toBe(1);
    // Form stays open — card still rendered, input not cleared by error.
    expect(screen.getByTestId('ext-card-tmdb')).toBeInTheDocument();
  });
});
