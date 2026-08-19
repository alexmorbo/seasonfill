import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { StatusBadge } from '@/components/StatusBadge';
import { KeywordChips } from '@/components/series-detail/KeywordChips';
import { CountryName } from '@/components/series-detail/CountryName';
import { LanguageName } from '@/components/series-detail/LanguageName';
import type { MovieDetail } from '@/api/movies';
import { formatMoney, isMoneyPresent } from '@/lib/money';

// MetaRow — one right-rail sidebar row (label + value), mirroring RailCard's
// RailRow. Local to the sidebar (page-level composition, not a section rebuild).
function MetaRow({
  label,
  value,
  accent,
  testId,
}: {
  label: string;
  value: ReactNode;
  accent?: boolean;
  testId?: string;
}) {
  return (
    <div
      data-testid={testId}
      className="flex items-center justify-between gap-3.5 py-[9px] text-[12.5px] border-b border-border-faint last:border-b-0"
    >
      <span className="text-tx-muted whitespace-nowrap">{label}</span>
      <span
        className={cn(
          'font-medium text-right min-w-0 inline-flex items-center gap-1.5',
          accent ? 'text-accent' : 'text-tx-secondary',
        )}
      >
        {value}
      </span>
    </div>
  );
}

// MovieSidebar — the right-rail metadata card, the movie analogue of RailCard.
// Reuses the series-detail leaves (StatusBadge / CountryName / LanguageName /
// KeywordChips) and the generic seriesDetail.rail.* labels (no movie-specific
// rail i18n keys exist). S5 adds the movie-specific rows: original_title (only
// when it differs from the display title), budget + revenue (formatted USD,
// hidden when 0 / undefined per ADR-0021 §S5).
export function MovieSidebar({ movie }: { movie: MovieDetail }) {
  const { t } = useTranslation();

  const country = movie.countries?.[0] ?? movie.country;
  const showStatus = Boolean(movie.status);
  const showStudio = Boolean(movie.studio);
  const showCountry = Boolean(country);
  const showLanguage = Boolean(movie.original_language);
  const keywords = movie.keywords ?? [];
  const showKeywords = keywords.length > 0;

  // S5 additions.
  const originalTitle = movie.original_title;
  const showOriginalTitle = Boolean(originalTitle) && originalTitle !== movie.title;
  const showBudget = isMoneyPresent(movie.budget);
  const showRevenue = isMoneyPresent(movie.revenue);

  if (
    !showStatus
    && !showStudio
    && !showCountry
    && !showLanguage
    && !showKeywords
    && !showOriginalTitle
    && !showBudget
    && !showRevenue
  ) {
    return null;
  }

  return (
    <div
      data-testid="movie-detail-sidebar"
      className={cn(
        'flex flex-col overflow-hidden rounded-lg border border-white/10 bg-bg-surface/40 backdrop-blur-md',
        'lg:sticky lg:top-[64px]',
      )}
    >
      <div className="px-4 pt-1 pb-1">
        {showStatus && (
          <MetaRow
            label={t('seriesDetail.rail.status')}
            testId="movie-detail-sidebar-status"
            value={<StatusBadge value={movie.status} />}
          />
        )}
        {showOriginalTitle && (
          <MetaRow
            label={t('movieDetail.meta.originalTitle')}
            testId="movie-detail-sidebar-original-title"
            value={(
              <span data-testid="movie-detail-sidebar-original-title-value">
                {originalTitle}
              </span>
            )}
          />
        )}
        {showStudio && (
          <MetaRow
            label={t('seriesDetail.rail.studio')}
            testId="movie-detail-sidebar-studio"
            value={<span data-testid="movie-detail-sidebar-studio-value">{movie.studio}</span>}
          />
        )}
        {showCountry && (
          <MetaRow
            label={t('seriesDetail.rail.country', { count: 1 })}
            testId="movie-detail-sidebar-country"
            value={<CountryName code={country} />}
          />
        )}
        {showLanguage && (
          <MetaRow
            label={t('seriesDetail.rail.originalLanguage')}
            testId="movie-detail-sidebar-language"
            value={<LanguageName code={movie.original_language} />}
          />
        )}
        {showBudget && (
          <MetaRow
            label={t('movieDetail.meta.budget')}
            testId="movie-detail-sidebar-budget"
            value={(
              <span
                className="tabular-nums"
                data-testid="movie-detail-sidebar-budget-value"
              >
                {formatMoney(movie.budget as number)}
              </span>
            )}
          />
        )}
        {showRevenue && (
          <MetaRow
            label={t('movieDetail.meta.revenue')}
            testId="movie-detail-sidebar-revenue"
            value={(
              <span
                className="tabular-nums"
                data-testid="movie-detail-sidebar-revenue-value"
              >
                {formatMoney(movie.revenue as number)}
              </span>
            )}
          />
        )}
      </div>
      {showKeywords && (
        <div
          data-testid="movie-detail-sidebar-keywords"
          className="border-t border-border-faint px-4 py-3.5"
        >
          <div className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint mb-2.5">
            {t('seriesDetail.overview.keywords')}
          </div>
          <KeywordChips chips={keywords} />
        </div>
      )}
    </div>
  );
}
