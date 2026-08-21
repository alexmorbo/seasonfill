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
  mode = 'edit' as 'edit' | 'create',
  type = 'sonarr' as 'sonarr' | 'radarr',
}: { mode?: 'edit' | 'create'; type?: 'sonarr' | 'radarr' }) {
  const qc = makeQc();
  const { control, register } = useForm({
    defaultValues: { ...FORM_DEFAULTS, type } as Record<string, unknown>,
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <WebhookSubCard
          control={control}
          mode={mode}
          instanceName={mode === 'edit' ? 'homelab' : undefined}
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

  it('BUG 3: always renders the override-url input (toggle removed)', () => {
    render(<Harness />);
    expect(
      screen.getByLabelText(/override base url|override base/i),
    ).toBeInTheDocument();
  });

  it('BUG 3: never renders the auto-install toggle switch', () => {
    render(<Harness />);
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('A3a: webhook title reads Radarr for a radarr-typed form', () => {
    // BUG 3 (ADR-0023 F1) removed the auto-install toggle row, which
    // carried the second "{arr} Connect" string this test used to
    // assert on — only the card's own webhookTitle string remains.
    render(<Harness type="radarr" />);
    expect(screen.getByTestId('webhook-subcard')).toHaveTextContent(/Webhook → Radarr/);
  });
});
