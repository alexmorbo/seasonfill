import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { RatingsStrip } from './RatingsStrip';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<RatingsStrip />', () => {
  it('renders both tokens when both scores are present', () => {
    r(<RatingsStrip rtRating={91} metacritic={69} />);
    expect(screen.getByTestId('ratings-strip')).toBeInTheDocument();
    expect(screen.getByTestId('rating-rt')).toHaveTextContent('🍅 91%');
    expect(screen.getByTestId('rating-mc')).toHaveTextContent('MC 69');
  });

  it('renders only the RT token when metacritic is absent', () => {
    r(<RatingsStrip rtRating={91} />);
    expect(screen.getByTestId('rating-rt')).toHaveTextContent('🍅 91%');
    expect(screen.queryByTestId('rating-mc')).toBeNull();
  });

  it('renders only the Metacritic token when RT is absent', () => {
    r(<RatingsStrip metacritic={69} />);
    expect(screen.queryByTestId('rating-rt')).toBeNull();
    expect(screen.getByTestId('rating-mc')).toHaveTextContent('MC 69');
  });

  it('renders nothing when both scores are absent', () => {
    const { container } = r(<RatingsStrip />);
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when omdbDegraded is true even with valid scores', () => {
    const { container } = r(
      <RatingsStrip rtRating={91} metacritic={69} omdbDegraded />,
    );
    expect(container.firstChild).toBeNull();
  });
});
