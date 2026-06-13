import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { RatingDuo, humanizeVotes } from './RatingDuo';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

describe('humanizeVotes', () => {
  it('humanizes thousands and millions', () => {
    expect(humanizeVotes(840)).toBe('840');
    expect(humanizeVotes(1200)).toBe('1.2k');
    expect(humanizeVotes(84_000)).toBe('84k');
    expect(humanizeVotes(1_200_000)).toBe('1.2M');
    expect(humanizeVotes(0)).toBe('');
  });
});

describe('<RatingDuo />', () => {
  it('renders both ratings when both are present', () => {
    r(<RatingDuo tmdb={{ score: 8.1, votes: 2100 }} imdb={{ score: 8.0, votes: 84000 }} />);
    expect(screen.getByTestId('rating-tmdb')).toBeInTheDocument();
    expect(screen.getByTestId('rating-imdb')).toBeInTheDocument();
    expect(screen.getByText('8.1')).toBeInTheDocument();
    expect(screen.getByText('8.0')).toBeInTheDocument();
  });

  it('hides imdb block when imdb rating is absent', () => {
    r(<RatingDuo tmdb={{ score: 7.5, votes: 1500 }} />);
    expect(screen.getByTestId('rating-tmdb')).toBeInTheDocument();
    expect(screen.queryByTestId('rating-imdb')).not.toBeInTheDocument();
  });

  it('returns null when neither rating is present', () => {
    const { container } = r(<RatingDuo />);
    expect(container.firstChild).toBeNull();
  });
});
