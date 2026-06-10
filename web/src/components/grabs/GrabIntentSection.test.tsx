import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { GrabIntentSection, type DecisionIntent } from './GrabIntentSection';

function wrap(ui: ReactElement): ReactNode {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('<GrabIntentSection />', () => {
  it('returns null when intent is null', () => {
    const { container } = render(wrap(<GrabIntentSection intent={null} />));
    expect(container.firstChild).toBeNull();
  });

  it('returns null when intent is undefined', () => {
    const { container } = render(wrap(<GrabIntentSection intent={undefined} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders reason badge with empty target/had — empty placeholders show', () => {
    const intent: DecisionIntent = {
      target_episodes: [],
      had_episodes: [],
      chosen_because: 'only_candidate',
      chosen_reason_detail: '',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    expect(screen.getByTestId('drawer-intent-section')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-intent-reason')).toHaveTextContent(
      /Only candidate|Единственный/i,
    );
    expect(screen.queryByTestId('drawer-intent-reason-detail')).toBeNull();
    // No E* chips
    expect(screen.queryByText(/^E\d+$/)).toBeNull();
  });

  it('renders full intent with target, had, reason, detail', () => {
    const intent: DecisionIntent = {
      target_episodes: [10, 11],
      had_episodes: [1, 2, 3],
      chosen_because: 'highest_score',
      chosen_reason_detail: 'score 88 vs alternates 64, 71',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    // Episode chips
    expect(screen.getByText('E10')).toBeInTheDocument();
    expect(screen.getByText('E11')).toBeInTheDocument();
    expect(screen.getByText('E1')).toBeInTheDocument();
    expect(screen.getByText('E2')).toBeInTheDocument();
    expect(screen.getByText('E3')).toBeInTheDocument();
    // Reason badge
    expect(screen.getByTestId('drawer-intent-reason')).toHaveTextContent(
      /Highest score|Лучший по баллам/i,
    );
    // Detail
    expect(screen.getByTestId('drawer-intent-reason-detail'))
      .toHaveTextContent('score 88 vs alternates 64, 71');
  });

  it('falls back to raw string for unknown chosen_because', () => {
    const intent: DecisionIntent = {
      target_episodes: [5],
      had_episodes: [],
      chosen_because: 'some_future_value',
      chosen_reason_detail: '',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    const badge = screen.getByTestId('drawer-intent-reason');
    expect(badge).toHaveTextContent('some_future_value');
  });

  it('truncates long had_episodes list and shows +N more chip', () => {
    const intent: DecisionIntent = {
      target_episodes: [13],
      had_episodes: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12],
      chosen_because: 'first_pass_quality',
      chosen_reason_detail: '',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    // First 8 visible
    for (const n of [1, 2, 3, 4, 5, 6, 7, 8]) {
      expect(screen.getByText(`E${n}`)).toBeInTheDocument();
    }
    // 9-12 not visible
    for (const n of [9, 10, 11, 12]) {
      expect(screen.queryByText(`E${n}`)).toBeNull();
    }
    // Overflow chip
    const overflow = screen.getByTestId('drawer-intent-had-overflow');
    expect(overflow).toHaveTextContent(/\+4|\+ещё 4/);
    expect(overflow).toHaveAttribute('title', 'E9, E10, E11, E12');
  });

  it('does not render overflow chip when had length <= 8', () => {
    const intent: DecisionIntent = {
      target_episodes: [],
      had_episodes: [1, 2, 3, 4, 5, 6, 7, 8],
      chosen_because: 'only_candidate',
      chosen_reason_detail: '',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    expect(screen.queryByTestId('drawer-intent-had-overflow')).toBeNull();
  });

  it('renders the watchdog_replay_unregistered chip with the i18n label', () => {
    const intent: DecisionIntent = {
      target_episodes: [],
      had_episodes: [],
      chosen_because: 'watchdog_replay_unregistered',
      chosen_reason_detail:
        'Watchdog re-grab of grab_abc via same GUID (tracker said unregistered)',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    // i18n test setup falls back to the en bundle.
    expect(screen.getByTestId('drawer-intent-reason')).toHaveTextContent(
      /re-grab/i,
    );
    expect(screen.getByTestId('drawer-intent-reason-detail')).toHaveTextContent(
      /same GUID/,
    );
  });

  it('renders the watchdog_replay_already_added chip with the i18n label', () => {
    const intent: DecisionIntent = {
      target_episodes: [],
      had_episodes: [],
      chosen_because: 'watchdog_replay_already_added',
      chosen_reason_detail:
        'Watchdog re-grab of grab_abc: qBit already had the hash',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    expect(screen.getByTestId('drawer-intent-reason')).toHaveTextContent(
      /already in qBit|уже в qBit/i,
    );
    expect(screen.getByTestId('drawer-intent-reason-detail')).toHaveTextContent(
      /already had the hash/,
    );
  });

  it('renders the watchdog_replay_error chip with the i18n label', () => {
    const intent: DecisionIntent = {
      target_episodes: [],
      had_episodes: [],
      chosen_because: 'watchdog_replay_error',
      chosen_reason_detail: 'Watchdog re-grab of grab_abc failed: sonarr 503',
    } as unknown as DecisionIntent;
    render(wrap(<GrabIntentSection intent={intent} />));
    expect(screen.getByTestId('drawer-intent-reason')).toHaveTextContent(
      /re-grab.*error|re-grab.*ошибка/i,
    );
    expect(screen.getByTestId('drawer-intent-reason-detail')).toHaveTextContent(
      /sonarr 503/,
    );
  });
});
