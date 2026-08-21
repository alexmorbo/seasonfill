import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ProvenanceChip } from '../ProvenanceChip';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<ProvenanceChip />', () => {
  it('renders nothing when provenance is undefined (series rows)', () => {
    const { container } = r(<ProvenanceChip provenance={undefined} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders "via Radarr" for radarr_search', () => {
    r(<ProvenanceChip provenance="radarr_search" />);
    expect(screen.getByTestId('torrent-provenance')).toHaveTextContent('via Radarr');
  });

  it('renders "manual import" for manual_import', () => {
    r(<ProvenanceChip provenance="manual_import" />);
    expect(screen.getByTestId('torrent-provenance')).toHaveTextContent('manual import');
  });

  it('falls back to the raw value for an unrecognized provenance', () => {
    r(<ProvenanceChip provenance="some_future_value" />);
    expect(screen.getByTestId('torrent-provenance')).toHaveTextContent('some_future_value');
  });
});
