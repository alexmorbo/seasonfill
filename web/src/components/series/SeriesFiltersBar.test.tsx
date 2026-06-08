import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from 'i18next';

import { SeriesFiltersBar, type SeriesFiltersValue } from './SeriesFiltersBar';
import { isDefaultFilters } from './seriesFilters';

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

describe('isDefaultFilters', () => {
  it('returns true for identical values', () => {
    expect(isDefaultFilters(DEFAULTS, DEFAULTS)).toBe(true);
  });
  it('returns false when search differs', () => {
    expect(isDefaultFilters({ ...DEFAULTS, search: 'x' }, DEFAULTS)).toBe(false);
  });
  it('returns false when state differs', () => {
    expect(isDefaultFilters({ ...DEFAULTS, state: 'all' }, DEFAULTS)).toBe(false);
  });
  it('returns false when networks differ in size', () => {
    expect(isDefaultFilters({ ...DEFAULTS, networks: new Set(['HBO']) }, DEFAULTS)).toBe(false);
  });
  it('returns false when networks differ in identity but equal size', () => {
    const a: SeriesFiltersValue = { ...DEFAULTS, networks: new Set(['HBO']) };
    const b: SeriesFiltersValue = { ...DEFAULTS, networks: new Set(['Netflix']) };
    expect(isDefaultFilters(a, b)).toBe(false);
  });
});

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

  it('does not render the reset button at default state', () => {
    renderBar();
    expect(screen.queryByTestId('series-filters-clear')).toBeNull();
  });

  it('renders the reset button when search differs', () => {
    renderBar({ value: { ...DEFAULTS, search: 'foo' } });
    expect(screen.getByTestId('series-filters-clear')).toBeInTheDocument();
  });

  it('renders the reset button when networks are selected', () => {
    renderBar({ value: { ...DEFAULTS, networks: new Set(['HBO']) } });
    expect(screen.getByTestId('series-filters-clear')).toBeInTheDocument();
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

  it('clicking the reset button invokes onClear', () => {
    const { onClear } = renderBar({ value: { ...DEFAULTS, search: 'foo' } });
    fireEvent.click(screen.getByTestId('series-filters-clear'));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it('opens the sort menu on trigger click and exposes all three options', async () => {
    const user = userEvent.setup();
    renderBar();
    await user.click(screen.getByTestId('series-filters-sort'));
    expect(await screen.findByTestId('series-filters-sort-updated')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-sort-title')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-sort-air-date')).toBeInTheDocument();
  });

  it('clicking the title sort option calls onChange with title_asc', async () => {
    const user = userEvent.setup();
    const { onChange } = renderBar();
    await user.click(screen.getByTestId('series-filters-sort'));
    await user.click(await screen.findByTestId('series-filters-sort-title'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'title_asc' }),
    );
  });

  it('clicking the air-date sort option calls onChange with air_date_desc', async () => {
    const user = userEvent.setup();
    const { onChange } = renderBar();
    await user.click(screen.getByTestId('series-filters-sort'));
    await user.click(await screen.findByTestId('series-filters-sort-air-date'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'air_date_desc' }),
    );
  });

  it('opens the network popover and renders all available networks', async () => {
    const user = userEvent.setup();
    renderBar({ networks: ['Apple TV+', 'HBO', 'Netflix'] });
    await user.click(screen.getByTestId('series-filters-networks'));
    expect(await screen.findByTestId('series-filters-networks-item-Apple TV+')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-networks-item-HBO')).toBeInTheDocument();
    expect(screen.getByTestId('series-filters-networks-item-Netflix')).toBeInTheDocument();
  });

  it('typing in the network search input filters the list', async () => {
    const user = userEvent.setup();
    renderBar({ networks: ['Apple TV+', 'HBO', 'Netflix'] });
    await user.click(screen.getByTestId('series-filters-networks'));
    const search = await screen.findByTestId('series-filters-networks-search');
    await user.type(search, 'net');
    expect(screen.queryByTestId('series-filters-networks-item-Apple TV+')).toBeNull();
    expect(screen.queryByTestId('series-filters-networks-item-HBO')).toBeNull();
    expect(screen.getByTestId('series-filters-networks-item-Netflix')).toBeInTheDocument();
  });

  it('clicking a network item calls onChange with the network added', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderBar({ networks: ['HBO', 'Netflix'], onChange });
    await user.click(screen.getByTestId('series-filters-networks'));
    await user.click(await screen.findByTestId('series-filters-networks-item-HBO'));
    expect(onChange).toHaveBeenCalledTimes(1);
    const firstCall = onChange.mock.calls[0];
    expect(firstCall).toBeDefined();
    const call = firstCall![0] as SeriesFiltersValue;
    expect(call.networks.has('HBO')).toBe(true);
  });

  it('clicking a selected chip removes the network', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderBar({
      value: { ...DEFAULTS, networks: new Set(['HBO']) },
      networks: ['HBO', 'Netflix'],
      onChange,
    });
    await user.click(screen.getByTestId('series-filters-networks'));
    await user.click(await screen.findByTestId('series-filters-networks-chip-HBO'));
    expect(onChange).toHaveBeenCalledTimes(1);
    const firstCall = onChange.mock.calls[0];
    expect(firstCall).toBeDefined();
    const call = firstCall![0] as SeriesFiltersValue;
    expect(call.networks.has('HBO')).toBe(false);
  });

  it('shows the empty-networks placeholder when none are available', async () => {
    const user = userEvent.setup();
    renderBar({ networks: [] });
    await user.click(screen.getByTestId('series-filters-networks'));
    expect(await screen.findByTestId('series-filters-networks-content')).toBeInTheDocument();
    expect(screen.queryByTestId('series-filters-networks-search')).toBeNull();
  });

  it('shows the no-matches hint when query matches nothing', async () => {
    const user = userEvent.setup();
    renderBar({ networks: ['HBO', 'Netflix'] });
    await user.click(screen.getByTestId('series-filters-networks'));
    const search = await screen.findByTestId('series-filters-networks-search');
    await user.type(search, 'zzz-nothing');
    expect(screen.queryByTestId('series-filters-networks-item-HBO')).toBeNull();
    expect(screen.queryByTestId('series-filters-networks-item-Netflix')).toBeNull();
  });
});
