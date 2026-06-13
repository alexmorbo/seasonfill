import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { ExternalLinksFooter } from './ExternalLinksFooter';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<ExternalLinksFooter /> — verified shape (I-1 contract)', () => {
  it('renders only entries whose IDs exist', () => {
    r(<ExternalLinksFooter links={{ imdb_id: 'tt9243946', tmdb_id: 1396 }} />);
    expect(screen.getByText('IMDb')).toBeInTheDocument();
    expect(screen.getByText('TMDB')).toBeInTheDocument();
    expect(screen.queryByText('TheTVDB')).not.toBeInTheDocument();
    expect(screen.queryByText('Homepage')).not.toBeInTheDocument();
  });

  it('returns null when no IDs exist', () => {
    const { container } = r(<ExternalLinksFooter links={{}} />);
    expect(container.firstChild).toBeNull();
  });
});
