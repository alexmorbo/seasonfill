import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { MovieSyncFooter } from './MovieSyncFooter';

// StaleBadge renders a Radix Tooltip — wrap in TooltipProvider (repo pattern).
function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

const SYNCED = '2026-08-18T10:00:00Z';

describe('<MovieSyncFooter />', () => {
  it('renders the synced-at line with no badge when nothing is stale', () => {
    r(<MovieSyncFooter syncedAt={SYNCED} />);
    const footer = screen.getByTestId('movie-sync-footer');
    expect(footer).toBeInTheDocument();
    expect(footer).toHaveTextContent(/Synced/);
    expect(screen.queryByTestId('stale-badge')).toBeNull();
  });

  it('renders a TMDB stale badge when tmdbStale', () => {
    r(<MovieSyncFooter syncedAt={SYNCED} tmdbStale />);
    expect(screen.getByTestId('stale-badge')).toHaveAttribute('data-source', 'tmdb');
  });

  it('renders an OMDb stale badge when omdbStale', () => {
    r(<MovieSyncFooter syncedAt={SYNCED} omdbStale />);
    expect(screen.getByTestId('stale-badge')).toHaveAttribute('data-source', 'omdb');
  });

  it('renders both badges when both sources are stale', () => {
    r(<MovieSyncFooter syncedAt={SYNCED} tmdbStale omdbStale />);
    expect(screen.getAllByTestId('stale-badge')).toHaveLength(2);
  });

  it('renders nothing without a syncedAt anchor', () => {
    const { container } = r(<MovieSyncFooter tmdbStale omdbStale />);
    expect(container.firstChild).toBeNull();
  });
});
