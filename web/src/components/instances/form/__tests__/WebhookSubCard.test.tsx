import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import i18n from '@/i18n';
import { WebhookSubCard } from '../WebhookSubCard';
import { FORM_DEFAULTS } from '@/components/settings/instance-form-helpers';

function makeQc() { return new QueryClient({ defaultOptions: { queries: { retry: false } } }); }

function Harness({
  installEnabled = true, mode = 'edit' as 'edit' | 'create',
}: { installEnabled?: boolean; mode?: 'edit' | 'create' }) {
  const qc = makeQc();
  const { control, register } = useForm({ defaultValues: FORM_DEFAULTS as Record<string, unknown> });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <WebhookSubCard
          control={control}
          mode={mode}
          instanceName={mode === 'edit' ? 'homelab' : undefined}
          installEnabled={installEnabled}
          register={register}
        />
      </I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<WebhookSubCard />', () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ installed: true, url: 'http://x' }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
  });

  it('shows the WebhookStatusBadge in edit mode (live status)', async () => {
    render(<Harness mode="edit" />);
    expect(await screen.findByTestId('webhook-status-badge')).toBeInTheDocument();
  });

  it('shows the static create-pill in create mode', () => {
    render(<Harness mode="create" />);
    expect(screen.getByTestId('webhook-create-pill')).toBeInTheDocument();
    expect(screen.queryByTestId('webhook-status-badge')).toBeNull();
  });

  it('hides the override-url input when install switch is off', () => {
    render(<Harness installEnabled={false} />);
    expect(screen.queryByLabelText(/override base url|override base/i)).toBeNull();
  });
});
