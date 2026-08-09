import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw,
  TriangleAlert,
  Sparkles,
  Flag,
  RotateCw,
  CalendarDays,
  ChevronLeft,
  ChevronRight,
} from 'lucide-react';

import {
  useCalendar,
  type CalendarEvent,
  type CalendarParams,
  type CalendarReport,
} from '@/api/calendar';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { MediaImage } from '@/components/MediaImage';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import { SubscribeCard } from '@/components/calendar/SubscribeCard';
import { cn } from '@/lib/utils';

// ── date helpers (local time) ────────────────────────────────────────────

function pad2(n?: number): string {
  return String(n ?? 0).padStart(2, '0');
}

function ymd(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

// weekBounds — Mon..Sun window around `now`, as YYYY-MM-DD strings.
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

function monthBounds(anchor: Date): { from: string; to: string } {
  const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
  const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
  return { from: ymd(first), to: ymd(last) };
}

// ── milestone + state presentation ───────────────────────────────────────

type MilestoneMeta = { Icon: typeof Sparkles; labelKey: string; className: string };

function milestoneMeta(m?: string | null): MilestoneMeta | null {
  if (m === 'premiere')
    return { Icon: Sparkles, labelKey: 'calendar.milestone.premiere', className: 'text-accent' };
  if (m === 'finale')
    return { Icon: Flag, labelKey: 'calendar.milestone.finale', className: 'text-info' };
  if (m === 'return')
    return { Icon: RotateCw, labelKey: 'calendar.milestone.return', className: 'text-ok' };
  return null;
}

function milestoneEmoji(m?: string | null): string {
  if (m === 'premiere') return '🎬';
  if (m === 'finale') return '🏁';
  if (m === 'return') return '🔄';
  return '';
}

function stateDotClass(state?: string): string | null {
  switch (state) {
    case 'downloaded':
      return 'bg-ok';
    case 'missing':
      return 'bg-danger';
    case 'upcoming':
      return 'bg-info';
    case 'followed_not_in_library':
      return 'bg-tx-muted';
    default:
      return null;
  }
}

function StatusDot({ state }: { state: string | undefined }) {
  const { t } = useTranslation();
  const dot = stateDotClass(state);
  if (!dot) return null;
  return (
    <span
      className={cn('inline-block size-2 shrink-0 rounded-full', dot)}
      title={t(`calendar.state.${state}`)}
      data-testid={`calendar-state-${state}`}
      aria-label={t(`calendar.state.${state}`)}
    />
  );
}

// ── event row (agenda) ───────────────────────────────────────────────────

function eventTestId(e: CalendarEvent): string {
  return `calendar-event-${e.series_id ?? 0}-S${pad2(e.season)}E${pad2(e.episode)}`;
}

function CalendarEventItem({ event }: { event: CalendarEvent }) {
  const { t } = useTranslation();
  const ms = milestoneMeta(event.milestone);
  const seriesId = event.series_id;
  const instances = event.in_library_instances ?? [];
  const title =
    event.title || (typeof seriesId === 'number' ? `#${seriesId}` : '—');

  return (
    <div
      className="flex items-center gap-3 rounded-md border border-border-faint bg-bg-base/40 px-3 py-2"
      data-testid={eventTestId(event)}
    >
      <div className="h-14 w-[38px] shrink-0 overflow-hidden rounded">
        <MediaImage
          hash={event.poster}
          kind="series_poster"
          title={title}
          fallback="monogram"
          className="w-full rounded"
        />
      </div>

      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {ms ? (
            <span
              className={cn('flex items-center gap-1 text-[12px] font-semibold', ms.className)}
              data-testid={`calendar-milestone-${event.milestone}`}
            >
              <ms.Icon className="size-3.5" />
              {t(ms.labelKey)}
            </span>
          ) : null}
          <StatusDot state={event.state} />
        </div>

        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {typeof seriesId === 'number' ? (
            <Link
              to={`/series/${seriesId}`}
              className="truncate text-[13px] font-medium text-tx-secondary hover:text-tx-primary hover:underline"
            >
              {title}
            </Link>
          ) : (
            <span className="truncate text-[13px] font-medium text-tx-secondary">{title}</span>
          )}
          <Badge variant="neutral" mono className="px-1.5 py-0 text-[10.5px]">
            S{pad2(event.season)}E{pad2(event.episode)}
          </Badge>
          {instances.map((inst) => (
            <Badge key={inst} variant="solid" mono className="px-1.5 py-0 text-[10px]">
              {inst}
            </Badge>
          ))}
        </div>
      </div>
    </div>
  );
}

// ── day group ────────────────────────────────────────────────────────────

function formatDayHeader(date: string, lang: string): string {
  // date is YYYY-MM-DD (UTC calendar date from the BE); render as local label.
  const [y, m, d] = date.split('-').map((x) => Number(x));
  const dt = new Date(y || 1970, (m || 1) - 1, d || 1);
  try {
    return new Intl.DateTimeFormat(lang, {
      weekday: 'short',
      day: 'numeric',
      month: 'short',
    }).format(dt);
  } catch {
    return date;
  }
}

function DayGroup({
  date,
  events,
  lang,
}: {
  date: string;
  events: readonly CalendarEvent[];
  lang: string;
}) {
  return (
    <div className="flex flex-col gap-2" data-testid={`calendar-day-${date}`}>
      <div className="text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
        {formatDayHeader(date, lang)}
      </div>
      <div className="flex flex-col gap-1.5">
        {events.map((e, i) => (
          <CalendarEventItem key={`${eventTestId(e)}-${i}`} event={e} />
        ))}
      </div>
    </div>
  );
}

// ── agenda view ──────────────────────────────────────────────────────────

function AgendaView({ report, lang }: { report: CalendarReport | undefined; lang: string }) {
  const { t } = useTranslation();
  const days = report?.days ?? [];
  const { from, to } = weekBounds();

  const thisWeek = days.filter((d) => (d.date ?? '') >= from && (d.date ?? '') <= to);
  const rest = days.filter((d) => !((d.date ?? '') >= from && (d.date ?? '') <= to));

  if (days.length === 0) {
    return (
      <Alert data-testid="calendar-empty">
        <CalendarDays className="size-4" />
        <AlertTitle>{t('calendar.empty')}</AlertTitle>
      </Alert>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="calendar-agenda">
      {thisWeek.length > 0 ? (
        <section className="flex flex-col gap-3" data-testid="calendar-this-week">
          <h2 className="text-[13px] font-semibold text-tx-primary">{t('calendar.thisWeek')}</h2>
          {thisWeek.map((d) => (
            <DayGroup key={d.date} date={d.date ?? ''} events={d.events ?? []} lang={lang} />
          ))}
        </section>
      ) : null}
      {rest.length > 0 ? (
        <section className="flex flex-col gap-3" data-testid="calendar-upcoming">
          <h2 className="text-[13px] font-semibold text-tx-primary">{t('calendar.upcoming')}</h2>
          {rest.map((d) => (
            <DayGroup key={d.date} date={d.date ?? ''} events={d.events ?? []} lang={lang} />
          ))}
        </section>
      ) : null}
    </div>
  );
}

// ── month grid view ──────────────────────────────────────────────────────

const MONTH_CELL_CHIPS = 3;

function MonthGrid({
  report,
  anchor,
}: {
  report: CalendarReport | undefined;
  anchor: Date;
}) {
  const byDate = useMemo(() => {
    const m = new Map<string, readonly CalendarEvent[]>();
    for (const d of report?.days ?? []) {
      if (d.date) m.set(d.date, d.events ?? []);
    }
    return m;
  }, [report]);

  const year = anchor.getFullYear();
  const month = anchor.getMonth();
  const first = new Date(year, month, 1);
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  const leading = (first.getDay() + 6) % 7; // Mon=0

  const cells: (string | null)[] = [];
  for (let i = 0; i < leading; i += 1) cells.push(null);
  for (let day = 1; day <= daysInMonth; day += 1) {
    cells.push(ymd(new Date(year, month, day)));
  }

  return (
    <div className="grid grid-cols-7 gap-1" data-testid="calendar-month-grid">
      {cells.map((date, i) => {
        if (date === null) {
          return <div key={`blank-${i}`} className="min-h-[76px] rounded-md" />;
        }
        const events = byDate.get(date) ?? [];
        const shown = events.slice(0, MONTH_CELL_CHIPS);
        const overflow = events.length - shown.length;
        return (
          <div
            key={date}
            className="flex min-h-[76px] flex-col gap-1 rounded-md border border-border-faint bg-bg-base/40 p-1.5"
            data-testid={`calendar-cell-${date}`}
          >
            <span className="text-[11px] font-medium text-tx-muted">
              {Number(date.split('-')[2])}
            </span>
            <div className="flex flex-col gap-0.5">
              {shown.map((e, j) => (
                <Link
                  key={`${eventTestId(e)}-${j}`}
                  to={typeof e.series_id === 'number' ? `/series/${e.series_id}` : '#'}
                  className="flex items-center gap-1 truncate text-[10.5px] text-tx-secondary hover:underline"
                  title={e.title ?? ''}
                  data-testid={eventTestId(e)}
                >
                  <span>{milestoneEmoji(e.milestone) || '·'}</span>
                  <StatusDot state={e.state} />
                  <span className="truncate">
                    S{pad2(e.season)}E{pad2(e.episode)}
                  </span>
                </Link>
              ))}
              {overflow > 0 ? (
                <span className="text-[10px] text-tx-faint">+{overflow}</span>
              ) : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ── page ─────────────────────────────────────────────────────────────────

export function Calendar() {
  const { t, i18n } = useTranslation();
  useSetPageTitle(t('calendar.title'));
  const { filter } = useInstanceFilter();

  const [view, setView] = useState<'agenda' | 'month'>('agenda');
  const [onlyLibrary, setOnlyLibrary] = useState(false);
  const [onlyPremieres, setOnlyPremieres] = useState(false);
  const [monthAnchor, setMonthAnchor] = useState(() => new Date());

  const params: CalendarParams = { onlyLibrary, onlyPremieres, lang: i18n.language };
  if (filter) params.instance = filter;
  if (view === 'month') {
    const mb = monthBounds(monthAnchor);
    params.from = mb.from;
    params.to = mb.to;
  }
  const query = useCalendar(params);
  const data = query.data;

  const monthLabel = useMemo(() => {
    try {
      return new Intl.DateTimeFormat(i18n.language, {
        month: 'long',
        year: 'numeric',
      }).format(monthAnchor);
    } catch {
      return `${monthAnchor.getFullYear()}-${pad2(monthAnchor.getMonth() + 1)}`;
    }
  }, [i18n.language, monthAnchor]);

  const stepMonth = (delta: number) =>
    setMonthAnchor((prev) => new Date(prev.getFullYear(), prev.getMonth() + delta, 1));

  return (
    <div className="flex flex-col gap-4" data-testid="calendar-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-[12.5px] text-tx-muted">{t('calendar.subtitle')}</p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
          data-testid="calendar-refresh"
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('calendar.refreshing') : t('calendar.refresh')}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1" data-testid="calendar-view-toggle">
          <Button
            variant={view === 'agenda' ? 'secondary' : 'ghost'}
            size="sm"
            onClick={() => setView('agenda')}
            data-testid="calendar-view-agenda"
          >
            {t('calendar.view.agenda')}
          </Button>
          <Button
            variant={view === 'month' ? 'secondary' : 'ghost'}
            size="sm"
            onClick={() => setView('month')}
            data-testid="calendar-view-month"
          >
            {t('calendar.view.month')}
          </Button>
        </div>

        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Button
            variant={onlyLibrary ? 'secondary' : 'ghost'}
            size="sm"
            aria-pressed={onlyLibrary}
            onClick={() => setOnlyLibrary((v) => !v)}
            data-testid="calendar-filter-only-library"
          >
            {t('calendar.filters.onlyLibrary')}
          </Button>
          <Button
            variant={onlyPremieres ? 'secondary' : 'ghost'}
            size="sm"
            aria-pressed={onlyPremieres}
            onClick={() => setOnlyPremieres((v) => !v)}
            data-testid="calendar-filter-only-premieres"
          >
            {t('calendar.filters.onlyPremieres')}
          </Button>
        </div>
      </div>

      <SubscribeCard />

      {view === 'month' ? (
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => stepMonth(-1)}
            data-testid="calendar-month-prev"
          >
            <ChevronLeft className="size-4" />
          </Button>
          <span
            className="min-w-[9rem] text-center text-[13px] font-medium capitalize text-tx-primary"
            data-testid="calendar-month-label"
          >
            {monthLabel}
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => stepMonth(1)}
            data-testid="calendar-month-next"
          >
            <ChevronRight className="size-4" />
          </Button>
        </div>
      ) : null}

      {query.isError ? (
        <Alert variant="destructive" data-testid="calendar-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('calendar.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div className="flex flex-col gap-3" data-testid="calendar-loading">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : view === 'agenda' ? (
        <AgendaView report={data} lang={i18n.language} />
      ) : (
        <Card className="p-3">
          <MonthGrid report={data} anchor={monthAnchor} />
        </Card>
      )}
    </div>
  );
}
