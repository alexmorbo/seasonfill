import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { LibraryStatusCard } from './LibraryStatusCard';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<LibraryStatusCard />', () => {
  it('renders progress, counts, size, missing chip', () => {
    r(<LibraryStatusCard
      library={{ episodes_on_disk: 42, episodes_total: 48, missing_count: 6, size_on_disk_bytes: 13_307_236_352, dominant_quality: 'WEB-DL 1080p' }}
    />);
    const bar = screen.getByTestId('library-progress');
    expect(bar.getAttribute('aria-valuenow')).toBe('88');
    expect(screen.getByText('42 of 48 on disk')).toBeInTheDocument();
    expect(screen.getByTestId('library-missing-chip')).toBeInTheDocument();
  });

  it('renders the nothing-on-disk message when total=0', () => {
    r(<LibraryStatusCard library={{ episodes_on_disk: 0, episodes_total: 0, missing_count: 0, size_on_disk_bytes: 0 }} />);
    expect(screen.getByText(/Nothing on disk yet/)).toBeInTheDocument();
  });

  it('renders the download chip when download is present', () => {
    r(<LibraryStatusCard
      library={{ episodes_on_disk: 1, episodes_total: 2, missing_count: 1, size_on_disk_bytes: 1024 }}
      download={{ status: 'downloading', title: 'S05E03 · 45%' }}
    />);
    expect(screen.getByTestId('library-download')).toBeInTheDocument();
    expect(screen.getByText('S05E03 · 45%')).toBeInTheDocument();
  });

  it('renders the recent events strip', () => {
    r(<LibraryStatusCard
      library={{ episodes_on_disk: 1, episodes_total: 2, missing_count: 1, size_on_disk_bytes: 1024 }}
      recent={[
        { event_type: 'imported', subject: 'S05E02', at: new Date(Date.now() - 7_200_000).toISOString() },
        { event_type: 'grabbed', subject: 'S05E03', at: new Date(Date.now() - 14_400_000).toISOString() },
      ]}
    />);
    expect(screen.getByTestId('library-recent')).toBeInTheDocument();
    expect(screen.getByText('S05E02')).toBeInTheDocument();
    expect(screen.getByText('S05E03')).toBeInTheDocument();
  });
});
