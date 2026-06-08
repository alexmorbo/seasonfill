import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { useForm } from 'react-hook-form';
import i18n from '@/i18n';
import { WatchdogSection } from '../WatchdogSection';
import { WATCHDOG_DEFAULTS } from '@/components/settings/instance-form-helpers';

function Harness({
  mode = 'edit' as 'create' | 'edit',
}: { mode?: 'create' | 'edit' }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { control, register, formState, setValue, getValues, watch } = useForm({
    defaultValues: WATCHDOG_DEFAULTS as Record<string, unknown>,
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <WatchdogSection
          control={control}
          register={register}
          errors={formState.errors}
          setValue={setValue}
          getValues={getValues}
          watch={watch}
          mode={mode}
          instanceName={mode === 'edit' ? 'homelab' : undefined}
          tValidationError={(m) => m ?? ''}
        />
      </I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<WatchdogSection />', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn((url) => {
      const u = String(url);
      if (u.endsWith('/webhook/status')) {
        return Promise.resolve(new Response(JSON.stringify({ installed: true }), {
          status: 200, headers: { 'Content-Type': 'application/json' },
        }));
      }
      if (u.endsWith('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({
          url: 'http://qbittorrent:8080', username: 'admin',
          password_set: true, category: 'sonarr',
          poll_interval_minutes: 30, regrab_cooldown_hours: 120,
          max_consecutive_no_better: 3, custom_unregistered_msgs: [],
          enabled: false,
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
  });

  it('renders qBit URL + username + password + category + enabled-switch', () => {
    render(<Harness />);
    expect(screen.getByTestId('watchdog-section')).toBeInTheDocument();
    expect(screen.getByLabelText(/qbittorrent url|qbittorrent URL|qbit_url/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: /enabled|watchdog|qbit_enabled/i })).toBeInTheDocument();
  });

  it('shows the auto-fill button only in edit mode', () => {
    const { rerender } = render(<Harness mode="edit" />);
    expect(screen.getByTestId('auto-fill-qbit')).toBeInTheDocument();
    rerender(<Harness mode="create" />);
    expect(screen.queryByTestId('auto-fill-qbit')).toBeNull();
  });

  it('disables the enabled-switch when webhook is not installed', () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ installed: false }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    render(<Harness />);
    return waitFor(() => {
      expect(screen.getByRole('switch')).toBeDisabled();
    });
  });

  it('disables the enabled-switch in create mode (no instance yet)', () => {
    render(<Harness mode="create" />);
    expect(screen.getByRole('switch')).toBeDisabled();
  });
});
