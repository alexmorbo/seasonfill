import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Copy, ExternalLink, GitBranch } from 'lucide-react';
import { Link } from 'react-router-dom';
import {
  Sheet, SheetContent, SheetHeader, SheetTitle,
} from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { ChipsRow } from '@/components/grabs/ChipsRow';
import { EpisodeFilesList } from '@/components/grabs/EpisodeFilesList';
import { buildChips, type Grab } from '@/lib/grabs/chipBuilder';
import { formatEpisodeRange } from '@/lib/grabs/format';
import { buildQbitDeepLink } from '@/lib/grabs/qbit';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { useQbitSettings } from '@/api/qbit';
import { useSourceDecisionID } from '@/lib/grabs/sourceDecisionLookup';
import { cn } from '@/lib/utils';

export interface GrabDrawerProps {
  readonly id: string | null;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly rows?: readonly Grab[] | undefined;
}

export function GrabDrawer({ id, open, onOpenChange, rows }: GrabDrawerProps) {
  const { t } = useTranslation();
  const q = useGrabs();
  const all = useMemo(() => rows ?? flattenGrabs(q.data?.pages), [rows, q.data]);
  const grab = id ? (all.find((x) => x.id === id) ?? null) : null;
  const instance = grab?.instance ?? null;

  // qBit settings — fetched lazily by useQbitSettings's natural enabled
  // gate (passes a string|null instance). The hook tolerates 404.
  const qbit = useQbitSettings(instance);
  const qbitUrl = qbit.data?.url ?? null;
  const deepLink = buildQbitDeepLink(qbitUrl, grab?.torrent_hash ?? null);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-[640px] overflow-y-auto p-0 bg-bg-surface"
        data-testid="grab-drawer-content"
      >
        <SheetHeader className="px-5 pt-5 pb-3 border-b border-border-faint">
          <SheetTitle className="text-[14px] font-semibold tracking-tight">
            {t('grabs.drawer.title')}
          </SheetTitle>
        </SheetHeader>
        {!grab ? (
          <div className="px-5 py-4">
            <EmptyState
              title={t('grabs.drawer.notFoundTitle')}
              body={t('grabs.drawer.notFoundBody')}
            />
          </div>
        ) : (
          <div className="px-5 py-4 flex flex-col gap-4">
            <DrawerHero grab={grab} />
            <DrawerReleaseSection grab={grab} />
            <DrawerTorrentSection grab={grab} deepLink={deepLink} />
            <DrawerFilesSection grab={grab} instance={instance} open={open} />
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[10px] font-semibold uppercase tracking-[0.09em] text-tx-faint">
      {children}
    </span>
  );
}

function DrawerHero({ grab }: { grab: Grab }) {
  const { t } = useTranslation();
  const ph = ((grab.series_id ?? 0) * 37) % 360;
  const epRange = formatEpisodeRange(
    grab.season_number ?? 0,
    undefined,
    grab.coverage_count ?? undefined,
  );
  return (
    <div className="flex gap-3">
      <div
        aria-hidden="true"
        className="w-[62px] h-[93px] rounded-md flex-none border border-border-subtle"
        style={{
          background: `radial-gradient(120% 80% at 30% 0%, oklch(0.34 0.07 ${ph}), oklch(0.19 0.04 ${(ph + 30) % 360}))`,
        }}
      />
      <div className="flex flex-col gap-1 min-w-0">
        <strong className="text-[16px] font-semibold tracking-tight truncate">
          {grab.series_title ?? '—'}
        </strong>
        <span className="text-[12px] text-tx-faint font-mono">
          {grab.coverage_count && grab.coverage_count > 1
            ? t('grabs.drawer.subtitle.fullSeason', { range: epRange })
            : epRange}
        </span>
        {grab.status && (
          <span className="self-start mt-1">
            <StatusBadge value={grab.status} />
          </span>
        )}
      </div>
    </div>
  );
}

function DrawerReleaseSection({ grab }: { grab: Grab }) {
  const { t } = useTranslation();
  const epRange = formatEpisodeRange(
    grab.season_number ?? 0,
    undefined,
    grab.coverage_count ?? undefined,
  );
  const chips = buildChips({ grab, episodeRangeLabel: epRange });
  return (
    <section className="flex flex-col gap-2">
      <SectionLabel>{t('grabs.drawer.release.label')}</SectionLabel>
      <div
        data-testid="drawer-release-raw"
        className={cn(
          'font-mono text-[11.5px] leading-relaxed text-tx-secondary',
          'bg-bg-base border border-border-faint rounded-md px-3 py-2',
          'break-words',
        )}
      >
        {grab.release_title ?? '—'}
      </div>
      <ChipsRow chips={chips} />
    </section>
  );
}

function truncateHash(hash: string): string {
  if (hash.length <= 12) return hash;
  return `${hash.slice(0, 8)}…${hash.slice(-4)}`;
}

function DrawerTorrentSection({
  grab, deepLink,
}: { grab: Grab; deepLink: string | null }) {
  const { t } = useTranslation();
  const decisionID = useSourceDecisionID({
    instance: grab.instance ?? null,
    scanRunID: grab.scan_run_id ?? null,
    seriesID: grab.series_id ?? null,
    seasonNumber: grab.season_number ?? null,
  });
  const sourceHref = grab.scan_run_id
    ? `/scans/${grab.scan_run_id}${decisionID ? `?drawer=${decisionID}` : ''}`
    : null;
  const hash = grab.torrent_hash;
  if (!hash) {
    return (
      <section className="flex flex-col gap-2">
        <SectionLabel>{t('grabs.drawer.torrent.label')}</SectionLabel>
        <p className="text-[12px] text-tx-faint italic">
          {t('grabs.drawer.torrent.unavailable')}
        </p>
        {sourceHref && (
          <div className="flex flex-wrap gap-2 mt-1">
            <SourceDecisionPill href={sourceHref} label={t('grabs.drawer.sourceDecision')} />
          </div>
        )}
      </section>
    );
  }
  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(hash);
      toast.success(t('grabs.drawer.copied'));
    } catch {
      toast.error(t('grabs.drawer.copyFailed'));
    }
  };
  return (
    <section className="flex flex-col gap-2">
      <SectionLabel>{t('grabs.drawer.torrent.label')}</SectionLabel>
      <div
        data-testid="drawer-hash-row"
        className={cn(
          'flex items-center gap-2 px-3 py-1.5 rounded-md',
          'bg-bg-base border border-border-faint',
          'font-mono text-[12px] text-tx-secondary',
        )}
      >
        <span className="text-tx-faint text-[10.5px]">hash</span>
        <span className="flex-1 truncate" title={hash}>{truncateHash(hash)}</span>
        <button
          type="button"
          onClick={onCopy}
          data-testid="drawer-hash-copy"
          aria-label={t('grabs.drawer.copy')}
          className={cn(
            'flex items-center gap-1 text-[11px] text-tx-muted',
            'hover:text-tx-primary transition-colors',
          )}
        >
          <Copy className="size-3" />
          {t('grabs.drawer.copy')}
        </button>
      </div>
      <div className="flex flex-wrap gap-2 mt-1">
        {deepLink ? (
          <a
            href={deepLink}
            target="_blank"
            rel="noopener noreferrer"
            data-testid="drawer-qbit-link"
            className={cn(
              'inline-flex items-center gap-1 px-2 py-1 rounded-[5px]',
              'border border-border-subtle bg-bg-surface-2',
              'text-[10.5px] font-mono font-semibold text-tx-secondary',
              'hover:bg-bg-surface-3 transition-colors',
            )}
          >
            <ExternalLink className="size-3" />
            {t('grabs.drawer.openInQbit')}
          </a>
        ) : (
          <span
            aria-disabled="true"
            data-testid="drawer-qbit-link-disabled"
            title={t('grabs.drawer.qbitUnavailable')}
            className={cn(
              'inline-flex items-center gap-1 px-2 py-1 rounded-[5px]',
              'border border-border-faint bg-bg-surface-2',
              'text-[10.5px] font-mono font-semibold text-tx-faint cursor-not-allowed',
            )}
          >
            <ExternalLink className="size-3" />
            {t('grabs.drawer.openInQbit')}
          </span>
        )}
        {sourceHref && <SourceDecisionPill href={sourceHref} label={t('grabs.drawer.sourceDecision')} />}
      </div>
    </section>
  );
}

function SourceDecisionPill({ href, label }: { href: string; label: string }) {
  return (
    <Link
      to={href}
      data-testid="drawer-decision-link"
      className={cn(
        'inline-flex items-center gap-1 px-2 py-1 rounded-[5px]',
        'border border-border-subtle bg-bg-surface-2',
        'text-[10.5px] font-mono font-semibold text-tx-secondary',
        'hover:bg-bg-surface-3 transition-colors',
      )}
    >
      <GitBranch className="size-3" />
      {label}
    </Link>
  );
}

function DrawerFilesSection({
  grab, instance, open,
}: { grab: Grab; instance: string | null; open: boolean }) {
  const { t } = useTranslation();
  return (
    <section className="flex flex-col gap-2">
      <SectionLabel>
        {t('grabs.drawer.files.label')}
      </SectionLabel>
      <EpisodeFilesList
        instance={instance}
        grabId={grab.id ?? null}
        grabStatus={grab.status ?? null}
        open={open}
      />
    </section>
  );
}
