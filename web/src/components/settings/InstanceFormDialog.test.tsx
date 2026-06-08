import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import type { ReactElement } from 'react';
import userEvent from '@testing-library/user-event';
import { render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { Toaster } from 'sonner';
import i18n from '@/i18n';
import { InstanceFormDialog } from '@/components/settings/InstanceFormDialog';

const origFetch = globalThis.fetch;

function wrap(node: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        {node}
        <Toaster />
      </QueryClientProvider>
    </I18nextProvider>
  );
}

interface FetchCall {
  url: string;
  method: string;
  body?: unknown;
}

interface FetchSetup {
  homelabBody?: Record<string, unknown>;
  qbitBody?: Record<string, unknown> | null;
  webhookInstalled?: boolean;
  qbitPutFails?: boolean;
  capture?: { calls: FetchCall[] };
}

function setupFetch({
  homelabBody,
  qbitBody,
  webhookInstalled = true,
  qbitPutFails = false,
  capture,
}: FetchSetup = {}) {
  globalThis.fetch = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const u = typeof url === 'string' ? url : url.toString();
    const method = (init?.method ?? 'GET').toUpperCase();
    if (capture) {
      let body: unknown;
      if (init?.body && typeof init.body === 'string') {
        try { body = JSON.parse(init.body); } catch { /* ignore */ }
      }
      capture.calls.push({ url: u, method, body });
    }
    if (u.endsWith('/instances/homelab') && method === 'GET') {
      return Promise.resolve(new Response(JSON.stringify({
        name: 'homelab',
        url: 'http://sonarr:80',
        api_key: '***',
        mode: 'auto',
        dry_run: null,
        public_url: 'https://s.arr.morbo.dev',
        webhook_install_enabled: true,
        ...(homelabBody ?? {}),
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.endsWith('/instances/homelab') && method === 'PUT') {
      return Promise.resolve(new Response(JSON.stringify({
        name: 'homelab',
        url: 'http://sonarr:80',
        api_key: '***',
        mode: 'auto',
        ...(homelabBody ?? {}),
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.endsWith('/instances') && method === 'POST') {
      return Promise.resolve(new Response(JSON.stringify({
        name: 'new-inst',
        url: 'http://sonarr:80',
        api_key: '***',
      }), { status: 201, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.endsWith('/webhook/status')) {
      return Promise.resolve(new Response(JSON.stringify({ installed: webhookInstalled }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }));
    }
    if (u.endsWith('/qbit/settings')) {
      if (method === 'GET') {
        if (qbitBody === null) {
          return Promise.resolve(new Response(JSON.stringify({
            error: 'not found', code: 'QBIT_SETTINGS_NOT_FOUND',
          }), { status: 404, headers: { 'Content-Type': 'application/json' } }));
        }
        return Promise.resolve(new Response(JSON.stringify(qbitBody ?? {
          url: 'http://qbittorrent:8080',
          username: 'admin',
          password_set: true,
          category: 'sonarr',
          poll_interval_minutes: 30,
          regrab_cooldown_hours: 120,
          max_consecutive_no_better: 3,
          custom_unregistered_msgs: [],
          enabled: true,
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (qbitPutFails) {
        return Promise.resolve(new Response(JSON.stringify({
          error: 'qbit unreachable', code: 'QBIT_UNREACHABLE',
        }), { status: 503, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }
    if (u.endsWith('/discover/qbit')) {
      return Promise.resolve(new Response(JSON.stringify({
        url: 'http://qbittorrent:8080',
        username: 'admin',
        category: 'sonarr',
        name: 'qbit',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    return Promise.resolve(new Response('{}', { status: 200 }));
  }) as typeof fetch;
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/instances', assign: vi.fn() },
  });
  setupFetch();
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<InstanceFormDialog /> redesign (F9)', () => {
  describe('shell', () => {
    it('renders the accordion shell with the Connection section open by default', async () => {
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      expect(await screen.findByTestId('connection-section')).toBeInTheDocument();
      // Tabs shell is gone — there is no role=tab.
      expect(screen.queryByRole('tab')).toBeNull();
      // Watchdog accordion item is closed by default; section body NOT rendered.
      expect(screen.queryByTestId('watchdog-section')).toBeNull();
    });

    it('renders the promoted Mode + Dry-run strip above the accordion', () => {
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="create" />,
      ));
      expect(screen.getByTestId('promoted-controls')).toBeInTheDocument();
    });

    it('renders the dirty footer with Save', () => {
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="create" />,
      ));
      expect(screen.getByTestId('dirty-footer')).toBeInTheDocument();
      expect(screen.getByTestId('dirty-footer-save')).toBeInTheDocument();
    });
  });

  describe('api_key dirty-bit (032b invariant)', () => {
    it('omits api_key from PUT body when the field is left blank in edit mode', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      const urlInput = await screen.findByLabelText(/^url$/i) as HTMLInputElement;
      await waitFor(() => expect(urlInput.value).toBe('http://sonarr:80'));
      await user.clear(urlInput);
      await user.type(urlInput, 'http://sonarr:8989');
      await user.click(screen.getByTestId('dirty-footer-save'));
      await waitFor(() => {
        const put = capture.calls.find(
          (c) => c.method === 'PUT' && c.url.endsWith('/instances/homelab'),
        );
        expect(put).toBeTruthy();
        expect((put!.body as Record<string, unknown>).api_key).toBeUndefined();
      });
    });

    it('includes api_key in PUT body when the user types a non-empty value', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      await user.type(screen.getByLabelText(/api key/i), 'NEW_KEY');
      await user.click(screen.getByTestId('dirty-footer-save'));
      await waitFor(() => {
        const put = capture.calls.find(
          (c) => c.method === 'PUT' && c.url.endsWith('/instances/homelab'),
        );
        expect(put).toBeTruthy();
        expect((put!.body as Record<string, unknown>).api_key).toBe('NEW_KEY');
      });
    });
  });

  describe('public_url empty-omit (041h-1 invariant)', () => {
    it('strips public_url from the wire payload when the field is left empty', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ homelabBody: { public_url: null }, capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      const urlInput = await screen.findByLabelText(/^url$/i) as HTMLInputElement;
      await user.clear(urlInput);
      await user.type(urlInput, 'http://sonarr:8989');
      await user.click(screen.getByTestId('dirty-footer-save'));
      await waitFor(() => {
        const put = capture.calls.find(
          (c) => c.method === 'PUT' && c.url.endsWith('/instances/homelab'),
        );
        expect(put).toBeTruthy();
        expect('public_url' in (put!.body as Record<string, unknown>)).toBe(false);
      });
    });
  });

  describe('qBit password dirty-bit (039d AC-4 invariant)', () => {
    it('routes Save through the orchestrator and sends qbit body with empty password when password not typed', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      // Give the in-flight detail + qBit GETs a tick to settle so the
      // useEffect reset() does not fire after the user starts typing.
      await new Promise((r) => setTimeout(r, 50));
      // Expand the watchdog accordion section.
      await user.click(await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i));
      const cat = await screen.findByLabelText(/^category$/i) as HTMLInputElement;
      await waitFor(() => expect(cat.value).toBe('sonarr'));
      // Type an extra character first to be sure the form is dirty even
      // if the value temporarily matches the default (RHF compares the
      // tracked value against the registered default on each keystroke).
      await user.clear(cat);
      // Type a value whose every prefix differs from the default
      // 'sonarr' — otherwise the interim 'sonarr' substring would
      // briefly mark the form clean and the parent useEffect would
      // reset() the input back to its registered default.
      await user.type(cat, 'newcat');
      // Wait for RHF to mark the form dirty.
      await waitFor(() =>
        expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument(),
      );
      await user.click(screen.getByTestId('dirty-footer-save'));
      await waitFor(() => {
        const put = capture.calls.find(
          (c) => c.method === 'PUT' && c.url.endsWith('/qbit/settings'),
        );
        expect(put).toBeTruthy();
        // Empty password = keep ciphertext server-side.
        expect((put!.body as Record<string, unknown>).password).toBe('');
        expect((put!.body as Record<string, unknown>).category).toBe('newcat');
      });
    });
  });

  describe('combined save orchestrator', () => {
    it('runs the instance PUT before the qBit PUT', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      await waitFor(() => {
        const gets = capture.calls.filter((c) => c.method === 'GET');
        expect(gets.some((c) => c.url.endsWith('/instances/homelab'))).toBe(true);
        expect(gets.some((c) => c.url.endsWith('/qbit/settings'))).toBe(true);
      });
      const urlInput = await screen.findByLabelText(/^url$/i) as HTMLInputElement;
      await waitFor(() => expect(urlInput.value).toBe('http://sonarr:80'));
      await user.clear(urlInput);
      await user.type(urlInput, 'http://sonarr:8989');
      await user.click(await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i));
      const cat = await screen.findByLabelText(/^category$/i) as HTMLInputElement;
      await waitFor(() => expect(cat.value).toBe('sonarr'));
      await user.clear(cat);
      // See note in the password test: avoid 'sonarr*' prefixes that
      // would briefly equal the default and trigger a reset() race.
      await user.type(cat, 'newcat');
      await waitFor(() =>
        expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument(),
      );
      await user.click(screen.getByTestId('dirty-footer-save'));
      await waitFor(() => {
        const writes = capture.calls.filter((c) => c.method === 'PUT');
        const idxInstance = writes.findIndex((c) => c.url.endsWith('/instances/homelab'));
        const idxQbit = writes.findIndex((c) => c.url.endsWith('/qbit/settings'));
        expect(idxInstance).toBeGreaterThanOrEqual(0);
        expect(idxQbit).toBeGreaterThanOrEqual(0);
        expect(idxInstance).toBeLessThan(idxQbit);
      });
    });

    it('toasts partial-success and keeps the dialog open when instance succeeds but qBit fails', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ qbitPutFails: true, capture });
      const onOpenChange = vi.fn();
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={onOpenChange} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      // Give in-flight GETs a tick to settle before typing.
      await new Promise((r) => setTimeout(r, 50));
      await user.click(await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i));
      const cat = await screen.findByLabelText(/^category$/i) as HTMLInputElement;
      await waitFor(() => expect(cat.value).toBe('sonarr'));
      await user.clear(cat);
      // See note in the password test: avoid 'sonarr*' prefixes that
      // would briefly equal the default and trigger a reset() race.
      await user.type(cat, 'xcat');
      await waitFor(() =>
        expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument(),
      );
      await user.click(screen.getByTestId('dirty-footer-save'));
      // Wait for the qBit PUT to be issued AND fail (instance PUT runs
      // first, qBit PUT second per orchestrator order).
      await waitFor(() => {
        const qbitPut = capture.calls.find(
          (c) => c.method === 'PUT' && c.url.endsWith('/qbit/settings'),
        );
        expect(qbitPut).toBeTruthy();
      });
      // The partial-success toast surfaces under sonner Toaster.
      await waitFor(() => {
        const toasts = document.querySelectorAll('[data-sonner-toast]');
        expect(toasts.length).toBeGreaterThan(0);
      });
      // Dialog must stay open: the form body is still mounted. The
      // orchestrator code path explicitly returns BEFORE calling
      // onOpenChange(false) on the partial-success branch.
      expect(screen.getByTestId('connection-section')).toBeInTheDocument();
    });
  });

  describe('invalid-section auto-jump', () => {
    it('keeps the connection section visible on invalid submit (create mode, no api_key)', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="create" />,
      ));
      await screen.findByTestId('connection-section');
      // Fill enough so the only invalid field is api_key.
      await user.type(screen.getByLabelText(/^name$/i), 'newinst');
      await user.type(screen.getByLabelText(/^url$/i), 'http://sonarr:80');
      await user.click(screen.getByTestId('dirty-footer-save'));
      // After the invalid-jump, connection section is still open and
      // shows the API-key field (the focused/erroring field).
      await waitFor(() => {
        const section = screen.getByTestId('connection-section');
        expect(within(section).getByLabelText(/api key/i)).toBeInTheDocument();
      });
    });
  });

  describe('F-P0-1 — qBit field editability + auto-fill button strictly user-initiated', () => {
    async function openWatchdogSection() {
      await screen.findByTestId('connection-section');
      // Give in-flight GETs a tick to settle so reset() does not
      // fire after the user starts typing.
      await new Promise((r) => setTimeout(r, 50));
      await userEvent.setup().click(
        await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i),
      );
    }

    it('typing into qbit_url is reflected in the input (no wipe) and fires no toast', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await openWatchdogSection();
      const urlInput = await screen.findByLabelText(/^qbittorrent url$/i) as HTMLInputElement;
      await waitFor(() => expect(urlInput.value).toBe('http://qbittorrent:8080'));
      await user.clear(urlInput);
      // Type a string whose every prefix differs from the default
      // so the form stays dirty throughout the keystroke sequence.
      await user.type(urlInput, 'http://other:1234');
      expect(urlInput.value).toBe('http://other:1234');
      // No auto-fill toast must have fired.
      const toasts = document.querySelectorAll('[data-sonner-toast]');
      expect(toasts.length).toBe(0);
    });

    it('typing into qbit_category is reflected and fires no toast', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await openWatchdogSection();
      const cat = await screen.findByLabelText(/^category$/i) as HTMLInputElement;
      await waitFor(() => expect(cat.value).toBe('sonarr'));
      await user.clear(cat);
      await user.type(cat, 'newcat');
      expect(cat.value).toBe('newcat');
      const toasts = document.querySelectorAll('[data-sonner-toast]');
      expect(toasts.length).toBe(0);
    });

    it('changing TuningSection cooldown mode does NOT fire any auto-fill toast', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      await new Promise((r) => setTimeout(r, 50));
      // Open Tuning section.
      await user.click(await screen.findByText(/настройки|тюнинг|tuning|поведение/i));
      // Switch cooldown mode to "strict" (segmented control radio).
      const strictRadio = await screen.findByRole('radio', { name: /strict|строгий/i });
      await user.click(strictRadio);
      // Wait a tick for any spurious effects to flush.
      await new Promise((r) => setTimeout(r, 50));
      const toasts = document.querySelectorAll('[data-sonner-toast]');
      expect(toasts.length).toBe(0);
    });

    it('AutoFill button fires exactly one toast per click and one network request', async () => {
      const capture = { calls: [] as FetchCall[] };
      setupFetch({ capture });
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await openWatchdogSection();
      // Confirm the default qbit_url is the discovered-equal value
      // first; then mutate so the click produces a real delta.
      const urlInput = await screen.findByLabelText(/^qbittorrent url$/i) as HTMLInputElement;
      await waitFor(() => expect(urlInput.value).toBe('http://qbittorrent:8080'));
      await user.clear(urlInput);
      await user.type(urlInput, 'http://stale:1');
      await user.click(screen.getByTestId('auto-fill-qbit'));
      await waitFor(() => {
        const toasts = document.querySelectorAll('[data-sonner-toast]');
        expect(toasts.length).toBe(1);
      });
      const discoverCalls = capture.calls.filter(
        (c) => c.method === 'GET' && c.url.endsWith('/discover/qbit'),
      );
      expect(discoverCalls.length).toBe(1);
    });

    it('AutoFill click that returns the already-present values produces no toast', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await openWatchdogSection();
      const urlInput = await screen.findByLabelText(/^qbittorrent url$/i) as HTMLInputElement;
      await waitFor(() => expect(urlInput.value).toBe('http://qbittorrent:8080'));
      // Defaults already equal what /discover/qbit will return (see
      // setupFetch default body). Click → onApply finds no delta →
      // no toast.
      await user.click(screen.getByTestId('auto-fill-qbit'));
      // Wait for the request to round-trip.
      await new Promise((r) => setTimeout(r, 80));
      const toasts = document.querySelectorAll('[data-sonner-toast]');
      expect(toasts.length).toBe(0);
    });
  });

  describe('N-3 — Tuning section persists across cooldown_mode toggles', () => {
    it('expanding Tuning then toggling cooldown_mode smart↔strict keeps Tuning open', async () => {
      const user = userEvent.setup();
      render(wrap(
        <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
      ));
      await screen.findByTestId('connection-section');
      // Give the in-flight detail + qBit GETs a tick to settle so the
      // initial re-seed effect runs to completion BEFORE the user
      // starts toggling. This matches the operator's real interaction.
      await new Promise((r) => setTimeout(r, 50));

      // Expand Tuning. The header text matches the same i18n key
      // tested elsewhere in this file (line 430).
      await user.click(await screen.findByText(/настройки|тюнинг|tuning|поведение/i));
      // Tuning section body is now in the DOM.
      expect(await screen.findByTestId('tuning-section')).toBeInTheDocument();

      // Toggle cooldown_mode strict → smart → strict. The segment uses
      // role="radio". Going back to "smart" matches the registered
      // default and would (pre-fix) flip isDirty to false, which would
      // re-fire the dialog's re-seed effect and collapse Tuning.
      const strict = await screen.findByRole('radio', { name: /strict|строгий/i });
      const smart = await screen.findByRole('radio', { name: /smart|умный/i });

      await user.click(strict);
      await new Promise((r) => setTimeout(r, 20));
      // Tuning must still be expanded.
      expect(screen.queryByTestId('tuning-section')).toBeInTheDocument();

      await user.click(smart);
      await new Promise((r) => setTimeout(r, 20));
      // This is the failure mode in N-3: pre-fix, Tuning collapses
      // here. Post-fix, the tuning-section testid is still mounted.
      expect(screen.queryByTestId('tuning-section')).toBeInTheDocument();

      await user.click(strict);
      await new Promise((r) => setTimeout(r, 20));
      expect(screen.queryByTestId('tuning-section')).toBeInTheDocument();
    });
  });

  describe('S074 — ?edit= deep-link populates the form even if detail arrives late', () => {
    it('shows the loading subtitle while detail is in flight, then hydrates the form', async () => {
      // Block the GET /instances/homelab response until we release it
      // so we can observe the loading-state DOM before hydration.
      let releaseDetail: ((body: Record<string, unknown>) => void) | null = null;
      const detailGate = new Promise<Record<string, unknown>>((res) => {
        releaseDetail = (b) => res(b);
      });
      globalThis.fetch = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
        const u = typeof url === 'string' ? url : url.toString();
        const method = (init?.method ?? 'GET').toUpperCase();
        if (u.endsWith('/instances/homelab') && method === 'GET') {
          return detailGate.then((body) => new Response(JSON.stringify(body), {
            status: 200, headers: { 'Content-Type': 'application/json' },
          }));
        }
        if (u.endsWith('/qbit/settings') && method === 'GET') {
          return Promise.resolve(new Response(JSON.stringify({
            url: 'http://qbittorrent:8080', username: 'admin', password_set: true,
            category: 'sonarr', poll_interval_minutes: 30,
            regrab_cooldown_hours: 120, max_consecutive_no_better: 3,
            custom_unregistered_msgs: [], enabled: true,
          }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
        }
        return Promise.resolve(new Response('{}', { status: 200 }));
      }) as typeof fetch;

      // Mimic the parent Instances page passing a name-only `initial`
      // before its detail fetch resolves: RHF gets name only, the
      // dialog's own useInstanceDetail('homelab') is enabled but
      // in-flight thanks to the gated fetch above.
      render(wrap(
        <InstanceFormDialog
          open
          onOpenChange={vi.fn()}
          mode="edit"
          initial={{ name: 'homelab' }}
        />,
      ));

      // Loading subtitle MUST include the instance name AND not the
      // create-branch i18n strings.
      await waitFor(() => {
        const sub = document.querySelector('[role="dialog"] p')?.textContent ?? '';
        expect(sub).toMatch(/homelab/);
        expect(sub.toLowerCase()).not.toMatch(/new sonarr|новый sonarr/);
      });
      // Save disabled while detail loads (editBlocked → button.disabled).
      const save = screen.getByTestId('dirty-footer-save') as HTMLButtonElement;
      expect(save.disabled).toBe(true);

      // Release the GET — form must now populate from the payload.
      releaseDetail!({
        name: 'homelab',
        url: 'http://sonarr:8989',
        api_key: '***',
        mode: 'auto',
        public_url: 'https://s.arr.morbo.dev',
        webhook_install_enabled: true,
      });

      const nameInput = await screen.findByLabelText(/^name$/i) as HTMLInputElement;
      await waitFor(() => expect(nameInput.value).toBe('homelab'));
      const urlInput = screen.getByLabelText(/^url$/i) as HTMLInputElement;
      expect(urlInput.value).toBe('http://sonarr:8989');
      await waitFor(() => {
        const sub = document.querySelector('[role="dialog"] p')?.textContent ?? '';
        expect(sub).toMatch(/homelab · http:\/\/sonarr:8989/);
      });
      await waitFor(() => {
        const s = screen.getByTestId('dirty-footer-save') as HTMLButtonElement;
        expect(s.disabled).toBe(false);
      });
    });

    it('populates correctly when detail resolves immediately (regression: populate effect not gated by openedRef)', async () => {
      // Default setupFetch responds synchronously. This locks in the
      // invariant that the populate effect is decoupled from the
      // section-seed gate — a future refactor that re-couples them
      // would break here.
      setupFetch();
      render(wrap(
        <InstanceFormDialog
          open
          onOpenChange={vi.fn()}
          mode="edit"
          initial={{ name: 'homelab' }}
        />,
      ));
      await screen.findByTestId('connection-section');
      const nameInput = await screen.findByLabelText(/^name$/i) as HTMLInputElement;
      await waitFor(() => expect(nameInput.value).toBe('homelab'));
    });
  });
});
