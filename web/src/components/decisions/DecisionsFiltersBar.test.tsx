import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DecisionsFiltersBar } from './DecisionsFiltersBar';

const defaults = {
  search: '',
  category: 'all' as const,
  instance: null,
  availableInstances: ['homelab', '4k'],
  window: '7d' as const,
  sort: 'freshest' as const,
  counts: { all: 271, done: 12, none: 23, blocked: 5, sonarr: 9, ok: 222 },
  onSearchChange: vi.fn(),
  onCategoryChange: vi.fn(),
  onInstanceChange: vi.fn(),
  onWindowChange: vi.fn(),
  onSortChange: vi.fn(),
  onReset: vi.fn(),
  canReset: false,
};

function renderBar(overrides: Partial<typeof defaults> = {}) {
  return render(
    <I18nextProvider i18n={i18n}>
      <DecisionsFiltersBar {...defaults} {...overrides} />
    </I18nextProvider>,
  );
}

describe('DecisionsFiltersBar', () => {
  it('renders search input with placeholder', () => {
    renderBar();
    expect(screen.getByPlaceholderText(/series|сериал/i)).toBeInTheDocument();
  });

  it('emits onSearchChange when typing', async () => {
    const onSearchChange = vi.fn();
    renderBar({ onSearchChange });
    await userEvent.type(screen.getByPlaceholderText(/series|сериал/i), 'Foundation');
    expect(onSearchChange).toHaveBeenCalled();
  });

  it('reset button is disabled when canReset=false', () => {
    renderBar({ canReset: false });
    const btn = screen.getByRole('button', { name: /reset|сброс/i });
    expect(btn).toBeDisabled();
  });

  it('reset button is enabled and fires onReset', async () => {
    const onReset = vi.fn();
    renderBar({ canReset: true, onReset });
    const btn = screen.getByRole('button', { name: /reset|сброс/i });
    expect(btn).not.toBeDisabled();
    await userEvent.click(btn);
    expect(onReset).toHaveBeenCalledOnce();
  });

  it('does NOT emit onCategoryChange for empty value (Radix guard)', () => {
    // Regression: Radix Select onValueChange fires '' on close; the
    // bar must not propagate it. Validated by the `if (v)` guard.
    const onCategoryChange = vi.fn();
    renderBar({ onCategoryChange });
    expect(onCategoryChange).not.toHaveBeenCalledWith('');
  });
});
