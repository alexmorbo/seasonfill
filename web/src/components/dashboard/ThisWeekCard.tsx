import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { useCalendar, type CalendarEvent } from '@/api/calendar';

function pad2(n?: number): string {
  return String(n ?? 0).padStart(2, '0');
}

function ymd(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

// startOfWeek/endOfWeek in local time as YYYY-MM-DD (Mon..Sun).
function weekBounds(now = new Date()): { from: string; to: string } {
  const d = new Date(now);
  const dow = (d.getDay() + 6) % 7; // Mon=0
  const mon = new Date(d);
  mon.setHours(0, 0, 0, 0);
  mon.setDate(d.getDate() - dow);
  const sun = new Date(mon);
  sun.setDate(mon.getDate() + 6);
  return { from: ymd(mon), to: ymd(sun) };
}

// ThisWeekCard — the dashboard rail «На этой неделе» widget. Shows this week's
// events (scope=all) as compact rows: milestone icon + SxxEyy + title. Mirrors
// FollowedCard's Card shell + empty/loading/error branches.
export function ThisWeekCard() {
  const { t, i18n } = useTranslation();
  const { from, to } = weekBounds();
  const { data, isLoading, isError } = useCalendar({ from, to, scope: 'all', lang: i18n.language });
  const events: CalendarEvent[] = (data?.days ?? []).flatMap((d) => d.events ?? []);

  return (
    <Card data-testid="this-week-card">
      <CardHeader className="p-4 pb-2">
        <CardTitle className="text-sm font-semibold">{t('calendar.thisWeek')}</CardTitle>
      </CardHeader>
      <CardContent className="p-4 pt-0">
        {isLoading && (
          <div className="flex flex-col gap-2" data-testid="this-week-loading">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-6 w-full" />
            ))}
          </div>
        )}
        {!isLoading && isError && (
          <p className="text-xs text-danger" data-testid="this-week-error">
            {t('common.error')}
          </p>
        )}
        {!isLoading && !isError && events.length === 0 && (
          <p className="text-xs text-tx-muted" data-testid="this-week-empty">
            {t('calendar.emptyWeek')}
          </p>
        )}
        {!isLoading && !isError && events.length > 0 && (
          <ul className="flex flex-col gap-1.5" data-testid="this-week-list">
            {events.slice(0, 6).map((e) => (
              <li key={`${e.series_id}-${e.season}-${e.episode}`} className="text-xs">
                <Link to={`/series/${e.series_id}`} className="hover:underline">
                  {milestoneIcon(e.milestone)}{' '}
                  <span className="font-mono">
                    S{pad2(e.season)}E{pad2(e.episode)}
                  </span>{' '}
                  {e.title}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function milestoneIcon(m?: string | null): string {
  if (m === 'premiere') return '🎬';
  if (m === 'finale') return '🏁';
  if (m === 'return') return '🔄';
  return '📺';
}
