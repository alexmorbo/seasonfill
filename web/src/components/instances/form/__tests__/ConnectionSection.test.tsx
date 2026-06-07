import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import i18n from '@/i18n';
import { ConnectionSection } from '../ConnectionSection';
import { FORM_DEFAULTS } from '@/components/settings/instance-form-helpers';

function Harness({
  mode = 'create' as 'create' | 'edit',
  uiUrlHint,
  onTest = vi.fn(),
}: {
  mode?: 'create' | 'edit';
  uiUrlHint?: string;
  onTest?: () => void;
}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { control, register, formState } = useForm<Record<string, unknown>>({
    defaultValues: FORM_DEFAULTS as Record<string, unknown>,
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <ConnectionSection
          control={control}
          register={register}
          errors={formState.errors}
          mode={mode}
          instanceName={mode === 'edit' ? 'homelab' : undefined}
          installEnabled
          uiUrlHint={uiUrlHint}
          onTest={onTest}
          testing={false}
          probeResult={null}
          tValidationError={(m) => m ?? ''}
        />
      </I18nextProvider>
    </QueryClientProvider>
  );
}

describe('<ConnectionSection />', () => {
  it('renders the Test button only in create mode', () => {
    render(<Harness mode="create" />);
    expect(screen.getByTestId('inst-test-button')).toBeInTheDocument();
  });

  it('omits the Test button in edit mode', () => {
    render(<Harness mode="edit" />);
    expect(screen.queryByTestId('inst-test-button')).toBeNull();
  });

  it('disables the Name input in edit mode', () => {
    render(<Harness mode="edit" />);
    expect((screen.getByLabelText(/name/i) as HTMLInputElement).disabled).toBe(true);
  });

  it('calls onTest when the Test button is clicked', async () => {
    const onTest = vi.fn();
    const user = userEvent.setup();
    render(<Harness onTest={onTest} />);
    await user.click(screen.getByTestId('inst-test-button'));
    expect(onTest).toHaveBeenCalledTimes(1);
  });

  it('shows the ui_url hint in edit mode', () => {
    render(<Harness mode="edit" uiUrlHint="https://s.arr.morbo.dev" />);
    expect(screen.getByTestId('inst-ui-url-hint')).toHaveTextContent('https://s.arr.morbo.dev');
  });
});
