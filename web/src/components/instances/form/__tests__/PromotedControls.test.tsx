import { describe, expect, it } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { useForm } from 'react-hook-form';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { PromotedControls } from '../PromotedControls';
import { FORM_DEFAULTS } from '@/components/settings/instance-form-helpers';

function Harness({ defaultValues = FORM_DEFAULTS as Record<string, unknown> }) {
  const { control } = useForm<Record<string, unknown>>({ defaultValues });
  return <PromotedControls control={control} />;
}

const wrap = (n: React.ReactElement) => <I18nextProvider i18n={i18n}>{n}</I18nextProvider>;

describe('<PromotedControls />', () => {
  it('renders Mode + Dry-run strips', () => {
    render(wrap(<Harness />));
    expect(screen.getByTestId('promoted-controls')).toBeInTheDocument();
    expect(screen.getAllByTestId('segmented-field')).toHaveLength(2);
  });

  it('switches mode via SegmentedField click', async () => {
    const user = userEvent.setup();
    render(wrap(<Harness />));
    const manual = screen.getByRole('radio', { name: /manual/i });
    await user.click(manual);
    expect(manual.getAttribute('data-state')).toBe('on');
  });

  it('default dry-run choice is "auto" (per FORM_DEFAULTS)', () => {
    render(wrap(<Harness />));
    // The "auto" button under the dry-run strip is the second strip's first option.
    const strips = screen.getAllByTestId('segmented-field');
    const dryStrip = strips[1]!;
    const autoBtn = dryStrip.querySelector('[data-value="auto"]') as HTMLElement;
    expect(autoBtn.getAttribute('data-state')).toBe('on');
  });
});
