import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  Ban,
  Copy,
  ExternalLink,
  GitBranch,
  ShieldCheck,
  ShieldOff,
  Timer,
  TriangleAlert,
} from 'lucide-react';
import { toast } from 'sonner';
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { EmptyState } from '@/components/EmptyState';
import { StatusBadge } from '@/components/StatusBadge';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import {
  useWatchdogSeriesDetail,
  type WatchdogSeriesDetail,
} from '@/lib/api/watchdogSeasons';
import { useRuntimeConfig } from '@/lib/runtime-config';
import {
  applyGuidRewrites,
  isTrackerUrl,
  type GuidRewriteRule,
} from '@/lib/guid-rewrite';
import type { components } from '@/api/schema';

type WatchdogSeriesSeason = components['schemas']['dto.WatchdogSeriesSeason'];
type WatchdogSeriesRecentDecision = components['schemas']['dto.WatchdogSeriesRecentDecision'];
type WatchdogSeriesRecentGrab = components['schemas']['dto.WatchdogSeriesRecentGrab'];

export interface WatchdogSeriesDrawerProps {
  readonly seriesID: number | null;
  readonly instance: string | null;
  readonly onOpenChange: (open: boolean) => void;
}

const INITIAL_VISIBLE = 5;
const EXPANDED_VISIBLE = 20;

export function WatchdogSeriesDrawer({
  seriesID,
  instance,
  onOpenChange,
}: WatchdogSeriesDrawerProps) {
  const { t } = useTranslation();
  const open = seriesID !== null && instance !== null;
  const q = useWatchdogSeriesDetail(instance, seriesID);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-[640px] overflow-y-auto p-0 bg-bg-surface"
        data-testid="watchdog-series-drawer"
      >
        <SheetHeader className="px-5 pt-5 pb-3 border-b border-border-faint">
          <SheetTitle className="text-[15px] font-semibold tracking-tight">
            {t('watchdog.drawer.title')}
          </SheetTitle>
        </SheetHeader>
        <div className="px-5 py-4 flex flex-col gap-4">
          {q.isPending ? (
            <DrawerSkeleton />
          ) : q.data ? (
            <DrawerBody data={q.data} />
          ) : (
            <EmptyState
              title={t('watchdog.drawer.empty')}
              body={null}
            />
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function DrawerSkeleton() {
  return (
    <div className="flex flex-col gap-3" data-testid="watchdog-series-drawer-loading">
      <Skeleton className="h-5 w-1/2" />
      <Skeleton className="h-4 w-1/3" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}

function DrawerBody({ data }: { data: WatchdogSeriesDetail }) {
  const { t } = useTranslation();
  const seasons = useMemo(() => {
    const list = (data.seasons ?? []).slice();
    list.sort((a, b) => (b.season_number ?? 0) - (a.season_number ?? 0));
    return list;
  }, [data.seasons]);

  if (seasons.length === 0) {
    return (
      <EmptyState
        title={t('watchdog.drawer.empty')}
        body={null}
      />
    );
  }

  const defaultValue = `s-${seasons[0]?.season_number ?? 0}`;

  return (
    <>
      <DrawerHero data={data} />
      <Accordion
        type="single"
        collapsible
        defaultValue={defaultValue}
        data-testid="watchdog-series-drawer-seasons"
      >
        {seasons.map((s) => (
          <AccordionItem
            key={s.season_number ?? 0}
            value={`s-${s.season_number ?? 0}`}
            data-testid={`watchdog-series-drawer-season-${s.season_number ?? 0}`}
          >
            <AccordionTrigger>
              <SeasonHeader season={s} />
            </AccordionTrigger>
            <AccordionContent className="flex flex-col gap-3 pb-4">
              <SeasonOrigin season={s} />
              <SeasonStats season={s} />
              <SeasonCooldown season={s} />
              <SeasonNoBetter season={s} />
              <SeasonDecisions season={s} />
              <SeasonGrabs season={s} />
            </AccordionContent>
          </AccordionItem>
        ))}
      </Accordion>
    </>
  );
}

function DrawerHero({ data }: { data: WatchdogSeriesDetail }) {
  const { t } = useTranslation();
  const title = data.series_title ?? `#${data.series_id ?? '?'}`;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 flex-wrap">
        <strong className="text-[16px] font-semibold tracking-tight text-tx-primary truncate">
          {title}
        </strong>
        {data.monitored ? (
          <Badge variant="ok" mono className="px-1.5 py-0.5 text-[10.5px]">
            <ShieldCheck className="h-3 w-3" />
            {t('watchdog.table.status.monitored')}
          </Badge>
        ) : (
          <Badge variant="neutral" mono className="px-1.5 py-0.5 text-[10.5px]">
            <ShieldOff className="h-3 w-3" />
            {t('watchdog.table.status.notMonitored')}
          </Badge>
        )}
      </div>
      <span className="text-[12px] text-tx-faint font-mono">
        {data.instance ?? '—'}
      </span>
    </div>
  );
}

function SeasonHeader({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const label = `S${String(season.season_number ?? 0).padStart(2, '0')}`;
  const missing = season.stats?.missing_aired_count ?? 0;
  return (
    <div className="flex items-center gap-2 flex-wrap min-w-0">
      <span className="font-mono text-[13px] font-semibold text-tx-primary">
        {label}
      </span>
      {missing > 0 && (
        <Badge variant="warn" mono className="px-1.5 py-0.5 text-[10.5px]">
          {t('watchdog.drawer.season.stats.missing')}: {missing}
        </Badge>
      )}
      {season.cooldown && (
        <Badge variant="warn" mono className="px-1.5 py-0.5 text-[10.5px]">
          <Timer className="h-3 w-3" />
          {t('watchdog.table.status.cooldownActive')}
        </Badge>
      )}
      {season.blacklist && (
        <Badge variant="danger" mono className="px-1.5 py-0.5 text-[10.5px]">
          <Ban className="h-3 w-3" />
          {t('watchdog.table.status.blacklisted')}
        </Badge>
      )}
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[10px] font-semibold uppercase tracking-[0.09em] text-tx-faint">
      {children}
    </span>
  );
}

function SeasonOrigin({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const o = season.origin;
  // Lazy-fetch runtime config — TanStack Query dedupes against the cached
  // Settings/General/etc. query, so even if the operator opens the drawer
  // without visiting Settings first this is at most one extra fetch and
  // does not block render (the link just stays hidden until the data lands).
  const runtime = useRuntimeConfig();
  const rules: readonly GuidRewriteRule[] =
    (runtime.data?.config.guid_rewrites ?? []).map((r) => ({
      from: r.from ?? '',
      to: r.to ?? '',
    }));
  // Build the tracker URL only when we have a GUID. Apply rewrites first;
  // only render the link when the rewritten string is http(s). An empty
  // rules list means we still render the link when Sonarr already stored
  // a public URL as the GUID.
  const trackerHref =
    o?.guid && isTrackerUrl(applyGuidRewrites(o.guid, rules))
      ? applyGuidRewrites(o.guid, rules)
      : null;
  const torrentHash = (o?.torrent_hash ?? '').trim();
  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-origin">
      <SectionLabel>{t('watchdog.drawer.origin.title')}</SectionLabel>
      {o ? (
        <div className="flex flex-col gap-0.5 text-[12px] text-tx-secondary">
          <span>
            <span className="text-tx-faint">{t('watchdog.drawer.origin.indexer')}: </span>
            <span className="font-medium text-tx-primary">{o.indexer ?? '—'}</span>
          </span>
          <span className="text-tx-faint">
            {t('watchdog.drawer.origin.firstSeen')}: {relativeTime(o.first_seen_at)}
          </span>
          <span className="text-tx-faint">
            {t('watchdog.drawer.origin.lastUsed')}: {relativeTime(o.last_used_at ?? o.last_seen_at)}
          </span>
          {torrentHash !== '' && (
            <OriginTorrentHashRow hash={torrentHash} />
          )}
          {trackerHref && (
            <a
              href={trackerHref}
              target="_blank"
              rel="noopener noreferrer"
              data-testid="drawer-origin-tracker-link"
              className="inline-flex items-center gap-1 text-tx-primary hover:underline mt-0.5"
            >
              <ExternalLink className="h-3 w-3" aria-hidden="true" />
              {t('watchdog.drawer.origin.openTracker')}
            </a>
          )}
        </div>
      ) : (
        <span className="text-[12px] text-tx-faint">—</span>
      )}
    </section>
  );
}

function OriginTorrentHashRow({ hash }: { hash: string }) {
  const { t } = useTranslation();
  const onCopy = async () => {
    if (!navigator.clipboard?.writeText) {
      toast.error(t('watchdog.drawer.origin.clipboardUnavailable'));
      return;
    }
    try {
      await navigator.clipboard.writeText(hash);
      toast.success(t('watchdog.drawer.origin.hashCopied'));
    } catch {
      toast.error(t('watchdog.drawer.origin.copyFailed'));
    }
  };
  return (
    <span
      data-testid="drawer-origin-torrent-hash"
      className="inline-flex items-center gap-1.5 mt-0.5 min-w-0"
    >
      <span className="text-tx-faint shrink-0">
        {t('watchdog.drawer.origin.infoHash')}:
      </span>
      <span
        className="font-mono text-tx-primary truncate min-w-0"
        title={hash}
        data-testid="drawer-origin-torrent-hash-value"
      >
        {truncateHash(hash)}
      </span>
      <button
        type="button"
        onClick={onCopy}
        data-testid="drawer-origin-torrent-hash-copy"
        aria-label={t('watchdog.drawer.origin.copyHash')}
        className={cn(
          'shrink-0 inline-flex items-center text-tx-muted',
          'hover:text-tx-primary transition-colors',
        )}
      >
        <Copy className="h-3 w-3" aria-hidden="true" />
      </button>
    </span>
  );
}

function truncateHash(hash: string): string {
  if (hash.length <= 12) return hash;
  return `${hash.slice(0, 8)}…${hash.slice(-4)}`;
}

function SeasonStats({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const s = season.stats;
  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-stats">
      <SectionLabel>{t('watchdog.drawer.season.title')}</SectionLabel>
      <div className="grid grid-cols-3 gap-2">
        <StatTile
          label={t('watchdog.drawer.season.stats.aired')}
          value={s?.aired_episode_count ?? 0}
        />
        <StatTile
          label={t('watchdog.drawer.season.stats.files')}
          value={s?.episode_file_count ?? 0}
        />
        <StatTile
          label={t('watchdog.drawer.season.stats.missing')}
          value={s?.missing_aired_count ?? 0}
          warn={(s?.missing_aired_count ?? 0) > 0}
        />
      </div>
    </section>
  );
}

function StatTile({
  label,
  value,
  warn = false,
}: {
  label: string;
  value: number;
  warn?: boolean;
}) {
  return (
    <div
      className={cn(
        'rounded-md border border-border-faint bg-bg-base px-2.5 py-2',
        'flex flex-col gap-0.5',
      )}
    >
      <span className="text-[9.5px] font-semibold uppercase tracking-[0.08em] text-tx-faint">
        {label}
      </span>
      <span
        className={cn(
          'font-mono text-[14px] font-semibold',
          warn ? 'text-status-warning' : 'text-tx-primary',
        )}
      >
        {value}
      </span>
    </div>
  );
}

function SeasonCooldown({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const cd = season.cooldown;
  let display = t('watchdog.drawer.cooldown.none');
  if (cd?.expires_at) {
    const ms = Date.parse(cd.expires_at);
    if (!Number.isNaN(ms)) {
      const d = new Date(ms);
      const dd = String(d.getDate()).padStart(2, '0');
      const mm = String(d.getMonth() + 1).padStart(2, '0');
      const hh = String(d.getHours()).padStart(2, '0');
      const mi = String(d.getMinutes()).padStart(2, '0');
      display = t('watchdog.drawer.cooldown.until', {
        ts: `${dd}.${mm} ${hh}:${mi}`,
      });
    }
  }
  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-cooldown">
      <SectionLabel>{t('watchdog.drawer.cooldown.title')}</SectionLabel>
      <span className="text-[12px] text-tx-secondary font-mono">{display}</span>
    </section>
  );
}

function SeasonNoBetter({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const nb = season.no_better_counter;
  const consec = nb?.consecutive ?? 0;
  const max = nb?.max ?? 3;
  const warn = max > 0 && consec >= Math.max(1, max - 1);
  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-nobetter">
      <SectionLabel>{t('watchdog.drawer.noBetter.title')}</SectionLabel>
      <Badge
        variant={warn ? 'warn' : 'neutral'}
        mono
        className="self-start px-1.5 py-0.5 text-[11px]"
      >
        {warn ? <TriangleAlert className="h-3 w-3" /> : null}
        {t('watchdog.drawer.noBetter.count', { consecutive: consec, max })}
      </Badge>
    </section>
  );
}

function SeasonDecisions({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const rows = season.recent_decisions ?? [];
  const cap = expanded ? EXPANDED_VISIBLE : INITIAL_VISIBLE;
  const visible = rows.slice(0, cap);
  const canExpand = rows.length > INITIAL_VISIBLE;

  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-decisions">
      <SectionLabel>{t('watchdog.drawer.decisions.title')}</SectionLabel>
      {rows.length === 0 ? (
        <span className="text-[12px] text-tx-faint italic">
          {t('watchdog.drawer.decisions.empty')}
        </span>
      ) : (
        <ul className="flex flex-col gap-1" data-testid="drawer-decisions-list">
          {visible.map((d, i) => (
            <DecisionRow key={d.id ?? `d-${i}`} d={d} />
          ))}
        </ul>
      )}
      {canExpand && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setExpanded((v) => !v)}
          data-testid="drawer-decisions-toggle"
          className="self-start text-[11.5px]"
        >
          {expanded
            ? t('watchdog.drawer.decisions.showLess')
            : t('watchdog.drawer.decisions.showMore')}
        </Button>
      )}
    </section>
  );
}

function DecisionRow({ d }: { d: WatchdogSeriesRecentDecision }) {
  return (
    <li
      className={cn(
        'flex items-center gap-2 text-[12px] text-tx-secondary',
        'border-b border-border-faint last:border-b-0 py-1',
      )}
    >
      <span className="font-mono text-[11px] text-tx-faint shrink-0 w-[88px] truncate">
        {relativeTime(d.created_at)}
      </span>
      <StatusBadge value={d.decision} mode="outcome" />
      <span className="truncate text-tx-muted">{d.reason ?? '—'}</span>
    </li>
  );
}

function SeasonGrabs({ season }: { season: WatchdogSeriesSeason }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const rows = season.recent_grabs ?? [];
  const cap = expanded ? EXPANDED_VISIBLE : INITIAL_VISIBLE;
  const visible = rows.slice(0, cap);
  const canExpand = rows.length > INITIAL_VISIBLE;

  return (
    <section className="flex flex-col gap-1.5" data-testid="drawer-section-grabs">
      <SectionLabel>{t('watchdog.drawer.grabs.title')}</SectionLabel>
      {rows.length === 0 ? (
        <span className="text-[12px] text-tx-faint italic">
          {t('watchdog.drawer.grabs.empty')}
        </span>
      ) : (
        <ul className="flex flex-col gap-1" data-testid="drawer-grabs-list">
          {visible.map((g, i) => (
            <GrabRow key={g.id ?? `g-${i}`} g={g} />
          ))}
        </ul>
      )}
      {canExpand && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setExpanded((v) => !v)}
          data-testid="drawer-grabs-toggle"
          className="self-start text-[11.5px]"
        >
          {expanded
            ? t('watchdog.drawer.grabs.showLess')
            : t('watchdog.drawer.grabs.showMore')}
        </Button>
      )}
    </section>
  );
}

function GrabRow({ g }: { g: WatchdogSeriesRecentGrab }) {
  const { t } = useTranslation();
  const href = g.id ? `/grabs?open=${encodeURIComponent(g.id)}` : null;
  const replay = Boolean(g.replay_of_id);
  return (
    <li
      className={cn(
        'flex items-center gap-2 text-[12px] text-tx-secondary',
        'border-b border-border-faint last:border-b-0 py-1',
      )}
    >
      <span className="font-mono text-[11px] text-tx-faint shrink-0 w-[88px] truncate">
        {relativeTime(g.created_at)}
      </span>
      <StatusBadge value={g.status} />
      {replay && (
        <Badge variant="neutral" mono className="px-1.5 py-0.5 text-[10.5px] shrink-0">
          <GitBranch className="h-3 w-3" />
          {t('watchdog.drawer.grabs.replayMarker')}
        </Badge>
      )}
      <span
        className="truncate font-mono text-[11.5px] text-tx-muted flex-1"
        title={g.release_title ?? ''}
      >
        {g.release_title ?? '—'}
      </span>
      {href && (
        <Link
          to={href}
          data-testid="drawer-grab-open"
          className={cn(
            'shrink-0 inline-flex items-center px-1.5 h-5 rounded',
            'border border-border-subtle bg-bg-surface-2',
            'text-[10.5px] font-mono font-semibold text-tx-secondary',
            'hover:bg-bg-surface-3 transition-colors',
          )}
        >
          {t('watchdog.drawer.grabs.openDrawer')}
        </Link>
      )}
    </li>
  );
}
