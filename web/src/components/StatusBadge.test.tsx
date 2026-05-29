import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { StatusBadge } from './StatusBadge';

function renderBadge(value: string | undefined, mode: 'status' | 'outcome' | 'health') {
  return render(
    <I18nextProvider i18n={i18n}>
      <StatusBadge value={value} mode={mode} />
    </I18nextProvider>,
  );
}

describe('StatusBadge', () => {
  let saved: string;
  beforeEach(() => { saved = i18n.resolvedLanguage ?? 'en'; });
  afterEach(async () => { await i18n.changeLanguage(saved); });

  it('renders em-dash for empty value', () => {
    renderBadge(undefined, 'status');
    expect(screen.getByText('—')).toBeInTheDocument();
  });

  it('translates outcome wire value in en', async () => {
    await i18n.changeLanguage('en');
    renderBadge('error', 'outcome');
    expect(screen.getByText('Error')).toBeInTheDocument();
  });

  it('translates outcome wire value in ru', async () => {
    await i18n.changeLanguage('ru');
    renderBadge('grab', 'outcome');
    expect(screen.getByText('Захват')).toBeInTheDocument();
  });

  it('falls back to raw value when i18n key missing', async () => {
    await i18n.changeLanguage('en');
    renderBadge('unknown_wire_value', 'status');
    expect(screen.getByText('unknown_wire_value')).toBeInTheDocument();
  });
});
