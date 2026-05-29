import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { OutcomeChips, OUTCOMES } from './OutcomeChips';

function renderChips(selected: Set<string>, onToggle = vi.fn()) {
  return {
    onToggle,
    ...render(
      <I18nextProvider i18n={i18n}>
        <OutcomeChips selected={selected} onToggle={onToggle} />
      </I18nextProvider>,
    ),
  };
}

describe('OutcomeChips', () => {
  it('renders every OUTCOMES member including error', () => {
    renderChips(new Set());
    expect(OUTCOMES).toContain('error');
    // each chip carries its wire value as test surface — we look it
    // up by aria-pressed attribute presence rather than label text
    // so the test stays locale-independent.
    const buttons = screen.getAllByRole('button');
    expect(buttons.length).toBe(OUTCOMES.length);
  });

  it('fires onToggle("error") when error chip clicked', async () => {
    const { onToggle } = renderChips(new Set());
    const user = userEvent.setup();
    // Label fallback is the wire value when an i18n key is missing,
    // and `outcomes.error` is shipped, so locale `en` renders "Error".
    const errChip = screen.getByRole('button', { name: /error/i });
    await user.click(errChip);
    expect(onToggle).toHaveBeenCalledWith('error');
  });

  it('marks error chip pressed when selected contains "error"', () => {
    renderChips(new Set(['error']));
    const errChip = screen.getByRole('button', { name: /error/i });
    expect(errChip).toHaveAttribute('aria-pressed', 'true');
  });
});
