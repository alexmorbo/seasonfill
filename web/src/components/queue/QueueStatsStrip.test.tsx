import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { QueueStatsStrip } from './QueueStatsStrip';

describe('<QueueStatsStrip />', () => {
  it('renders the three stats when monitored is provided', () => {
    renderWithProviders(
      <QueueStatsStrip backlogCount={10} missingEpisodes={294} monitoredTotal={147} />,
    );
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.getByText('294')).toBeInTheDocument();
    expect(screen.getByText('147')).toBeInTheDocument();
  });

  it('hides the monitored stat when undefined', () => {
    renderWithProviders(
      <QueueStatsStrip backlogCount={10} missingEpisodes={294} monitoredTotal={undefined} />,
    );
    expect(screen.queryByText('147')).not.toBeInTheDocument();
  });

  it('renders zeros without crashing', () => {
    renderWithProviders(
      <QueueStatsStrip backlogCount={0} missingEpisodes={0} monitoredTotal={0} />,
    );
    expect(screen.getAllByText('0').length).toBeGreaterThanOrEqual(2);
  });
});
