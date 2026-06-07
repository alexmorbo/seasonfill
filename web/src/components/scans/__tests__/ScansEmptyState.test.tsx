import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ScansEmptyState } from '../ScansEmptyState';

const wrap = (n: React.ReactElement) => <I18nextProvider i18n={i18n}>{n}</I18nextProvider>;

describe('<ScansEmptyState />', () => {
  it('calls onReset when the CTA is clicked', async () => {
    const onReset = vi.fn();
    const user = userEvent.setup();
    render(wrap(<ScansEmptyState onReset={onReset} />));
    await user.click(screen.getByRole('button'));
    expect(onReset).toHaveBeenCalledTimes(1);
  });
});
