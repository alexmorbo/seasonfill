import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesFiltersBar, type SeriesFiltersValue } from './SeriesFiltersBar';

if (!i18n.isInitialized) {
  void i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  });
}

const DEFAULTS: SeriesFiltersValue = {
  search: '',
  state: 'missing',
  sort: 'updated_desc',
  monitoredOnly: true,
  networks: new Set<string>(),
};

function renderBar(overrides: Partial<{
  value: SeriesFiltersValue;
  networks: readonly string[];
  onChange: (v: SeriesFiltersValue) => void;
  onClear: () => void;
}> = {}) {
  const onChange = overrides.onChange ?? vi.fn();
  const onClear = overrides.onClear ?? vi.fn();
  render(
    <I18nextProvider i18n={i18n}>
      <SeriesFiltersBar
        value={overrides.value ?? DEFAULTS}
        availableNetworks={overrides.networks ?? ['Apple TV+', 'HBO', 'Netflix']}
        defaults={DEFAULTS}
        onChange={onChange}
        onClear={onClear}
      />
    </I18nextProvider>,
  );
  return { onChange, onClear };
}

describe('<SeriesFiltersBar />', () => {
  it('renders all five filter controls', () => {
    renderBar();
    expect(screen.getByTestId('series-filters-search')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-state')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-networks')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-monitored')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-sort')).toBeInTheDocument();
  });

  it('renders the sort trigger as a button (not a combobox)', () => {
    renderBar();
    const sort = screen.getByTestId('series-filters-sort');
    expect(sort.tagName).toBe('BUTTON');
    expect(sort.getAttribute('role')).not.toBe('combobox');
  });

  it('clear button is disabled at default state', () => {
    renderBar();
    expect(screen.getByTestId('series-filters-clear')).toBeDisabled();
  });

  it('clear button enables when search differs', () => {
    renderBar({ value: { ...DEFAULTS, search: 'foo' } });
    expect(screen.getByTestId('series-filters-clear')).not.toBeDisabled();
  });

  it('typing in search calls onChange with new search value', () => {
    const { onChange } = renderBar();
    const input = screen.getByTestId('series-filters-search') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'mankind' } });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ search: 'mankind' }),
    );
  });

  it('clear button invokes onClear', () => {
    const { onClear } = renderBar({ value: { ...DEFAULTS, search: 'foo' } });
    fireEvent.click(screen.getByTestId('series-filters-clear'));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it('opens the sort menu on trigger click and exposes both options', async () => {
    const user = userEvent.setup();
    renderBar();
    await user.click(screen.getByTestId('series-filters-sort'));
    expect(await screen.findByTestId('series-filters-sort-updated')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-sort-title')).toBeInTheDocument();
  });

  it('clicking an alternative sort option calls onChange with the new sort', async () => {
    const user = userEvent.setup();
    const { onChange } = renderBar();
    await user.click(screen.getByTestId('series-filters-sort'));
    await user.click(await screen.findByTestId('series-filters-sort-title'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'title_asc' }),
    );
  });
});
