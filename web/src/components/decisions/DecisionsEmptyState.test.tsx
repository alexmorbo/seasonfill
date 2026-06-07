import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DecisionsEmptyState } from './DecisionsEmptyState';

describe('DecisionsEmptyState', () => {
  it('renders the filter-empty title and body', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <DecisionsEmptyState onReset={vi.fn()} />
      </I18nextProvider>,
    );
    expect(screen.getByTestId('decisions-empty-state')).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: /filter|фильтр/i }),
    ).toBeInTheDocument();
  });

  it('reset button calls onReset', async () => {
    const onReset = vi.fn();
    render(
      <I18nextProvider i18n={i18n}>
        <DecisionsEmptyState onReset={onReset} />
      </I18nextProvider>,
    );
    await userEvent.click(screen.getByRole('button', { name: /reset|сброс/i }));
    expect(onReset).toHaveBeenCalledOnce();
  });
});
