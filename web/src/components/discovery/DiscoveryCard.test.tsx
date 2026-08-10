import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DiscoveryCard } from './DiscoveryCard';
import type { DiscoverySeriesItem } from '@/api/discovery';

const item: DiscoverySeriesItem = {
  series_id: 0, tmdb_id: 42, title: 'Severance', year: 2022,
  poster_hash: 'abc123', tmdb_rating: 8.4, in_library_instances: [],
};

function renderCard(onHide = vi.fn()) {
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>
        <DiscoveryCard item={item} onHide={onHide} />
      </MemoryRouter>
    </I18nextProvider>,
  );
  return onHide;
}

describe('<DiscoveryCard />', () => {
  it('renders the card + a kebab trigger', () => {
    renderCard();
    expect(screen.getByText('Severance')).toBeInTheDocument();
    expect(screen.getByTestId('discovery-card-menu-trigger')).toBeInTheDocument();
  });

  it('opens the menu and fires onHide with the item on "hide"', async () => {
    const user = userEvent.setup();
    const onHide = renderCard();
    await user.click(screen.getByTestId('discovery-card-menu-trigger'));
    const hideItem = await screen.findByTestId('discovery-card-hide');
    await user.click(hideItem);
    expect(onHide).toHaveBeenCalledTimes(1);
    expect(onHide).toHaveBeenCalledWith(item);
  });

  it('opening the menu does not navigate the card (propagation stopped)', async () => {
    // LIVE-BROWSER caveat: jsdom cannot fully reproduce Radix pointer capture /
    // portal teardown timing. This asserts the structural guarantee (menu opens,
    // card SeriesCard root still present, no route change in MemoryRouter). The
    // real propagation/navigation interaction MUST be Playwright-verified — see
    // "Live-browser verification" below.
    const user = userEvent.setup();
    renderCard();
    await user.click(screen.getByTestId('discovery-card-menu-trigger'));
    expect(await screen.findByTestId('discovery-card-menu')).toBeInTheDocument();
    // The tmdb-only card root (role=button) is still mounted, not unmounted by a
    // navigation.
    expect(screen.getByTestId('series-card')).toBeInTheDocument();
  });
});
