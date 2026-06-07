import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { HeroGreeting } from './HeroGreeting';

function renderHero(
  grabs: number | null = null,
  imports: number | null = null,
  fails: number | null = null,
  avg7d: number | null = null,
  quietLastImport: string | null | undefined = undefined,
  now: Date = new Date(),
) {
  return render(
    <I18nextProvider i18n={i18n}>
      <HeroGreeting
        now={now}
        grabs={grabs}
        imports={imports}
        fails={fails}
        avg7d={avg7d}
        quietLastImport={quietLastImport}
      />
    </I18nextProvider>,
  );
}

describe('<HeroGreeting />', () => {
  beforeEach(() => {
    vi.setSystemTime(new Date('2026-06-07T09:00:00Z'));
  });

  it('renders morning greeting when hour < 12', () => {
    vi.setSystemTime(new Date('2026-06-07T08:30:00Z'));
    renderHero(10, 5, 0, 8);
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    expect(screen.getByText(/10.*5.*0/)).toBeInTheDocument();
  });

  it('renders afternoon greeting when 12 <= hour < 18', () => {
    vi.setSystemTime(new Date('2026-06-07T15:00:00Z'));
    renderHero(10, 5, 0, 8);
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    expect(screen.getByText(/10.*5.*0/)).toBeInTheDocument();
  });

  it('renders evening greeting when hour >= 18', () => {
    vi.setSystemTime(new Date('2026-06-07T20:00:00Z'));
    renderHero(10, 5, 0, 8);
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    expect(screen.getByText(/10.*5.*0/)).toBeInTheDocument();
  });

  it('renders quiet-day copy when quietLastImport is provided (not undefined)', () => {
    renderHero(
      undefined,
      undefined,
      undefined,
      undefined,
      '2 hours ago',
    );
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    expect(screen.getByText(/2 hours ago/i)).toBeInTheDocument();
  });

  it('renders greeting-only when counters are null (data loading)', () => {
    renderHero(null, null, null, null, undefined);
    expect(screen.getByTestId('hero-greeting')).toBeInTheDocument();
    expect(screen.queryByText(/grabs/)).not.toBeInTheDocument();
  });

  it('renders summary with grabs, imports, fails when all counters present', () => {
    renderHero(25, 12, 1, 20);
    expect(screen.getByText(/25.*12.*1/)).toBeInTheDocument();
  });

  it('renders aboveAvg trend when grabs / avg7d >= 1.2', () => {
    renderHero(24, 10, 0, 20);
    expect(screen.getByText(/24.*10.*0/)).toBeInTheDocument();
  });

  it('renders belowAvg trend when grabs / avg7d <= 0.8', () => {
    renderHero(8, 4, 1, 20);
    expect(screen.getByText(/8.*4.*1/)).toBeInTheDocument();
  });

  it('renders atAvg trend when 0.8 < grabs / avg7d < 1.2', () => {
    renderHero(16, 8, 0, 20);
    expect(screen.getByText(/16.*8.*0/)).toBeInTheDocument();
  });

  it('renders atAvg when avg7d <= 0 and grabs === 0', () => {
    renderHero(0, 0, 0, 0);
    expect(screen.getByText(/0.*0.*0/)).toBeInTheDocument();
  });

  it('renders aboveAvg when avg7d <= 0 and grabs > 0', () => {
    renderHero(1, 1, 0, 0);
    expect(screen.getByText(/1.*1.*0/)).toBeInTheDocument();
  });
});
