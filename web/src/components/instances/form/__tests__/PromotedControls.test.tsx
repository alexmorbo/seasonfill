import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { useForm } from 'react-hook-form';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { PromotedControls } from '../PromotedControls';
import { FORM_DEFAULTS } from '@/components/settings/instance-form-helpers';

function Harness({
  defaultValues = FORM_DEFAULTS as Record<string, unknown>,
  mode = 'create' as 'create' | 'edit',
  onTypeChange,
}: {
  defaultValues?: Record<string, unknown>;
  mode?: 'create' | 'edit';
  onTypeChange?: (v: 'sonarr' | 'radarr') => void;
}) {
  const { control } = useForm<Record<string, unknown>>({ defaultValues });
  return <PromotedControls control={control} mode={mode} onTypeChange={onTypeChange} />;
}

const wrap = (n: React.ReactElement) => <I18nextProvider i18n={i18n}>{n}</I18nextProvider>;

describe('<PromotedControls />', () => {
  it('renders Type + Mode + Dry-run strips in create mode', () => {
    render(wrap(<Harness />));
    expect(screen.getByTestId('promoted-controls')).toBeInTheDocument();
    expect(screen.getByTestId('promoted-type')).toBeInTheDocument();
    // Type + Mode + Dry-run = three segmented strips in create mode.
    expect(screen.getAllByTestId('segmented-field')).toHaveLength(3);
  });

  it('type selector defaults to "sonarr" (per FORM_DEFAULTS)', () => {
    render(wrap(<Harness />));
    const typeStrip = screen
      .getByTestId('promoted-type')
      .querySelector('[data-testid="segmented-field"]') as HTMLElement;
    const sonarrBtn = typeStrip.querySelector('[data-value="sonarr"]') as HTMLElement;
    expect(sonarrBtn.getAttribute('data-state')).toBe('on');
  });

  it('switches type to radarr via SegmentedField click', async () => {
    const user = userEvent.setup();
    render(wrap(<Harness />));
    const radarr = screen.getByRole('radio', { name: /radarr/i });
    await user.click(radarr);
    expect(radarr.getAttribute('data-state')).toBe('on');
  });

  it('edit mode renders a read-only type badge (no type selector)', () => {
    render(
      wrap(
        <Harness
          mode="edit"
          defaultValues={{ ...(FORM_DEFAULTS as Record<string, unknown>), type: 'radarr' }}
        />,
      ),
    );
    // Read-only badge shown; the type SegmentedField is absent → only Mode +
    // Dry-run strips remain.
    expect(screen.getByTestId('promoted-type-readonly')).toBeInTheDocument();
    expect(screen.getByTestId('promoted-type-readonly')).toHaveTextContent(
      i18n.t('settings.instances.form.type.radarr'),
    );
    expect(screen.getAllByTestId('segmented-field')).toHaveLength(2);
  });

  it('BUG 2: calls onTypeChange with the new type when the segmented control changes', async () => {
    const onTypeChange = vi.fn();
    const user = userEvent.setup();
    render(wrap(<Harness onTypeChange={onTypeChange} />));
    await user.click(screen.getByRole('radio', { name: /radarr/i }));
    expect(onTypeChange).toHaveBeenCalledWith('radarr');
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
    const dryStrip = screen
      .getByTestId('promoted-controls')
      .querySelectorAll('[data-testid="segmented-field"]');
    // Within promoted-controls: [0] = mode, [1] = dry-run.
    const autoBtn = dryStrip[1]!.querySelector('[data-value="auto"]') as HTMLElement;
    expect(autoBtn.getAttribute('data-state')).toBe('on');
  });
});
