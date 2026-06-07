import { describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { ScansFirstRunState } from '../ScansFirstRunState';

const wrap = (n: React.ReactElement) => (
  <I18nextProvider i18n={i18n}><MemoryRouter>{n}</MemoryRouter></I18nextProvider>
);

describe('<ScansFirstRunState />', () => {
  it('renders the title + body + two CTA buttons', () => {
    render(wrap(<ScansFirstRunState onTriggerScan={vi.fn()} />));
    expect(screen.getByTestId('scans-first-run')).toBeInTheDocument();
    expect(screen.getAllByRole('button')).toHaveLength(2);
  });

  it('calls onTriggerScan when the primary CTA is clicked', async () => {
    const cb = vi.fn();
    const user = userEvent.setup();
    render(wrap(<ScansFirstRunState onTriggerScan={cb} />));
    await user.click(screen.getAllByRole('button')[0]!);
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
