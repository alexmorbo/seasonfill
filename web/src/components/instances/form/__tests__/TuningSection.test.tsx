import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { useForm } from 'react-hook-form';
import i18n from '@/i18n';
import { TuningSection } from '../TuningSection';
import { FORM_DEFAULTS } from '@/components/settings/instance-form-helpers';

function Harness() {
  const { control, register, formState } = useForm<Record<string, unknown>>({
    defaultValues: FORM_DEFAULTS as Record<string, unknown>,
  });
  return (
    <I18nextProvider i18n={i18n}>
      <TuningSection
        control={control}
        register={register}
        errors={formState.errors}
        tValidationError={(m) => m ?? ''}
      />
    </I18nextProvider>
  );
}

describe('<TuningSection />', () => {
  it('renders the cooldown segmented control with Smart + Strict only', () => {
    render(<Harness />);
    const tuning = screen.getByTestId('tuning-section');
    const segs = tuning.querySelectorAll('[data-testid="segmented-field"]');
    const cooldown = segs[0]!;
    expect(cooldown.querySelectorAll('button')).toHaveLength(2);
    expect(cooldown.querySelector('[data-value="smart"]')).toBeTruthy();
    expect(cooldown.querySelector('[data-value="strict"]')).toBeTruthy();
  });

  it('renders the advanced sub-block heading', () => {
    render(<Harness />);
    expect(screen.getByTestId('tuning-advanced')).toBeInTheDocument();
  });

  it('renders the skip-anime toggle row', () => {
    render(<Harness />);
    expect(screen.getByRole('switch', { name: /anime|аниме/i })).toBeInTheDocument();
  });
});
