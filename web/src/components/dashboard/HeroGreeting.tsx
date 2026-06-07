import { useTranslation } from 'react-i18next';

type Phase = 'morning' | 'afternoon' | 'evening';

function phaseFor(date: Date): Phase {
  const h = date.getHours();
  if (h < 12) return 'morning';
  if (h < 18) return 'afternoon';
  return 'evening';
}

function trendKey(today: number, avg: number): 'aboveAvg' | 'belowAvg' | 'atAvg' {
  if (avg <= 0) return today > 0 ? 'aboveAvg' : 'atAvg';
  const r = today / avg;
  if (r >= 1.2) return 'aboveAvg';
  if (r <= 0.8) return 'belowAvg';
  return 'atAvg';
}

export interface HeroGreetingProps {
  readonly now?: Date;
  readonly grabs: number | null;
  readonly imports: number | null;
  readonly fails: number | null;
  readonly avg7d: number | null;
  readonly quietLastImport?: string | null | undefined;
}

export function HeroGreeting({ now, grabs, imports, fails, avg7d, quietLastImport }: HeroGreetingProps) {
  const { t } = useTranslation();
  const greet = t(`dashboard.greet.${phaseFor(now ?? new Date())}`);

  if (quietLastImport !== undefined) {
    return (
      <div data-testid="hero-greeting" className="flex flex-wrap items-center gap-4 mb-5">
        <span className="text-[15px] text-tx-secondary">
          <b className="text-tx-primary font-semibold">{greet}</b>{' '}
          {t('dashboard.today.quiet', { when: quietLastImport ?? '—' })}
        </span>
      </div>
    );
  }
  if (grabs === null || imports === null || fails === null || avg7d === null) {
    return (
      <div data-testid="hero-greeting" className="flex flex-wrap items-center gap-4 mb-5">
        <span className="text-[15px] text-tx-secondary">
          <b className="text-tx-primary font-semibold">{greet}</b>
        </span>
      </div>
    );
  }
  const trend = t(`dashboard.today.${trendKey(grabs, avg7d)}`);
  const summary = t('dashboard.today.summary', { grabs, imports, fails, trend });
  return (
    <div data-testid="hero-greeting" className="flex flex-wrap items-center gap-4 mb-5">
      <span className="text-[15px] text-tx-secondary">
        <b className="text-tx-primary font-semibold">{greet}</b> {summary}
      </span>
    </div>
  );
}
