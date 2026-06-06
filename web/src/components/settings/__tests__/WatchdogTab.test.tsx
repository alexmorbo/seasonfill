import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { WatchdogTab } from '../WatchdogTab';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

const origFetch = globalThis.fetch;
beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/instances', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

// Reusable fetch router — each test composes URL → response mapping
// to avoid the boilerplate of inline switch statements.
interface RouteMap {
  readonly [key: string]: (init?: RequestInit) => Response | Promise<Response>;
}

function mockFetch(routes: RouteMap) {
  globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof u === 'string' ? u : u.toString();
    for (const [pattern, handler] of Object.entries(routes)) {
      if (url.includes(pattern)) return handler(init);
    }
    return jsonResp({ error: 'no route match: ' + url }, 500);
  }) as typeof fetch;
}

describe('<WatchdogTab />', () => {
  it('renders the install-webhook banner when settings are absent (404)', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    expect(await screen.findByTestId('watchdog-webhook-gate')).toBeVisible();
    expect(
      screen.getByRole('button', { name: /install webhook/i }),
    ).toBeEnabled();
  });

  it('clicking Install fires POST /webhook/install and flips the banner', async () => {
    let installCalled = false;
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
      '/webhook/install': () => {
        installCalled = true;
        return jsonResp({ installed: true, created: true, notification_id: 7 }, 201);
      },
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    await userEvent.click(
      await screen.findByRole('button', { name: /install webhook/i }),
    );
    await waitFor(() => expect(installCalled).toBe(true));
    expect(
      await screen.findByTestId('watchdog-webhook-installed'),
    ).toBeVisible();
  });

  it('shows the public-url link inline when install returns 412', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
      '/webhook/install': () =>
        jsonResp({ error: 'no url', code: 'PUBLIC_URL_UNCONFIGURED' }, 412),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    await userEvent.click(
      await screen.findByRole('button', { name: /install webhook/i }),
    );
    expect(await screen.findByTestId('watchdog-public-url-link')).toBeVisible();
    // Banner stayed in the destructive state.
    expect(screen.getByTestId('watchdog-webhook-gate')).toBeVisible();
  });

  it('password placeholder shows "set" indicator when password_set:true', async () => {
    mockFetch({
      '/qbit/settings': () =>
        jsonResp(
          {
            instance_name: 'alpha',
            url: 'http://q',
            category: 'sonarr',
            password_set: true,
            enabled: false,
            poll_interval_minutes: 30,
            regrab_cooldown_hours: 120,
            max_consecutive_no_better: 3,
            custom_unregistered_msgs: [],
          },
          200,
        ),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    const pwInput = await screen.findByLabelText(/password/i);
    await waitFor(() => {
      expect(pwInput).toHaveAttribute('placeholder', expect.stringMatching(/set/i));
    });
  });

  it('Auto-fill populates url/username/category and leaves password empty', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
      '/discover/qbit': () =>
        jsonResp(
          { url: 'http://discovered:8080', username: 'admin', category: 'sonarr', name: 'qbit' },
          200,
        ),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    await userEvent.click(
      await screen.findByRole('button', { name: /auto-fill from sonarr/i }),
    );
    await waitFor(() => {
      expect(screen.getByLabelText(/qbittorrent url/i)).toHaveValue('http://discovered:8080');
    });
    expect(screen.getByLabelText(/^username$/i)).toHaveValue('admin');
    // Password input stays empty.
    expect(screen.getByLabelText(/password/i)).toHaveValue('');
  });

  it('Save sends the canonical PUT body with empty password (preserve)', async () => {
    let putBody: string | undefined;
    mockFetch({
      '/qbit/settings': (init) => {
        if (init?.method === 'PUT') {
          putBody = typeof init.body === 'string' ? init.body : undefined;
          return jsonResp({ instance_name: 'alpha', url: 'http://q', password_set: true }, 200);
        }
        return jsonResp(
          {
            instance_name: 'alpha',
            url: 'http://q',
            category: 'sonarr',
            password_set: true,
            enabled: false,
            poll_interval_minutes: 30,
            regrab_cooldown_hours: 120,
            max_consecutive_no_better: 3,
            custom_unregistered_msgs: [],
          },
          200,
        );
      },
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    // Wait for the form to seed from GET, then make ONE dirtying change.
    await screen.findByDisplayValue('http://q');
    const urlInput = screen.getByLabelText(/qbittorrent url/i);
    await userEvent.clear(urlInput);
    await userEvent.type(urlInput, 'http://q2');
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(putBody).toBeDefined());
    const parsed = JSON.parse(putBody ?? '{}');
    expect(parsed.url).toBe('http://q2');
    expect(parsed.password).toBe(''); // preserve dirty-bit semantic
  });

  it('Enabled Switch is disabled when webhook is not installed', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    const sw = await screen.findByLabelText(/^enabled$/i);
    expect(sw).toBeDisabled();
  });

  it('Enabled Switch becomes interactive after webhook install', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
      '/webhook/install': () =>
        jsonResp({ installed: true, created: true, notification_id: 9 }, 201),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    await userEvent.click(
      await screen.findByRole('button', { name: /install webhook/i }),
    );
    await screen.findByTestId('watchdog-webhook-installed');
    const sw = screen.getByLabelText(/^enabled$/i);
    await waitFor(() => expect(sw).not.toBeDisabled());
  });

  it('contains NO Radix Select element (guards against the empty-value bug)', async () => {
    mockFetch({
      '/qbit/settings': () => jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
    });
    renderWithProviders(<WatchdogTab instanceName="alpha" />);
    const form = await screen.findByTestId('watchdog-form');
    // shadcn Select trigger is rendered as a combobox/button by Radix.
    expect(within(form).queryAllByRole('combobox')).toHaveLength(0);
  });
});
