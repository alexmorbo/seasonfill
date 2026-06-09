import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { AlertCircle, Copy, ExternalLink, GitBranch } from 'lucide-react';
import { Link } from 'react-router-dom';
import {
  Sheet, SheetContent, SheetHeader, SheetTitle,
} from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { ChipsRow } from '@/components/grabs/ChipsRow';
import { EpisodeFilesList } from '@/components/grabs/EpisodeFilesList';
import { GrabIntentSection } from '@/components/grabs/GrabIntentSection';
import { buildChips, type Grab } from '@/lib/grabs/chipBuilder';
import { formatEpisodeRange } from '@/lib/grabs/format';
import { isKubeInternalHost } from '@/lib/grabs/qbit';
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
  //
  // 083 / F-P2-1: prefer the explicit browser-reachable URL. If empty,
  // fall back to `url` only when it's NOT kube-internal — otherwise
  // the link would 404 in the operator's browser and we hide it.
  //
  // qBT's Web UI has no SPA route for an individual torrent hash; the
  // best we can do is open the root and let the operator find the row
  // in their session. We therefore link to the bare base URL.
  const qbit = useQbitSettings(instance);
  const publicUrl = (qbit.data?.qbit_public_url ?? '').trim();
  const fallbackUrl = (qbit.data?.url ?? '').trim();
  let qbitUrl: string | null = null;
  if (publicUrl !== '') {
    qbitUrl = publicUrl;
  } else if (fallbackUrl !== '' && !isKubeInternalHost(fallbackUrl)) {
    qbitUrl = fallbackUrl;
  }
  const qbitHref = qbitUrl ? qbitUrl.replace(/\/+$/, '') : null;

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
            <DrawerErrorSection grab={grab} />
            <DrawerReleaseSection grab={grab} />
            <GrabIntentSection intent={grab.intent ?? null} />
            <DrawerTorrentSection grab={grab} qbitHref={qbitHref} />
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
  grab, qbitHref,
}: { grab: Grab; qbitHref: string | null }) {
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
        {qbitHref ? (
          <a
            href={qbitHref}
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

// DrawerErrorSection renders the full upstream error_message for a
// failed grab. The list row (GrabRow.tsx:138-148) shows a one-line
// preview clamped at 420px; the drawer renders the full text with
// preserved whitespace and a copy button. Mirrors DecisionDrawer's
// ErrorDetailSection for visual consistency.
//
// Gated on grab.error_message being non-empty — non-failed grabs
// (`imported`, `grabbed`) typically have no error text and skip
// the section entirely.
function DrawerErrorSection({ grab }: { grab: Grab }) {
  const { t } = useTranslation();
  const text = grab.error_message;
  if (!text) return null;

  const onCopy = async () => {
    if (!navigator.clipboard?.writeText) {
      toast.error(t('decisions.detail.clipboardUnavailable'));
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t('decisions.detail.copied'));
    } catch {
      toast.error(t('decisions.detail.copyFailed'));
    }
  };

  return (
    <section
      data-testid="drawer-error-section"
      aria-labelledby="grab-error-heading"
      className={cn(
        'border border-danger/30 rounded-md p-4',
        'bg-danger-dim flex flex-col gap-2.5',
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <AlertCircle className="w-3.5 h-3.5 text-danger" aria-hidden="true" />
          <h4
            id="grab-error-heading"
            className="text-[12px] font-semibold uppercase tracking-[0.06em] text-danger"
          >
            {t('grabs.drawer.error.heading')}
          </h4>
        </div>
        <button
          type="button"
          onClick={onCopy}
          data-testid="drawer-error-copy"
          aria-label={t('decisions.detail.copy')}
          className={cn(
            'inline-flex items-center gap-1 px-1.5 h-6 rounded',
            'border border-border-faint text-[11px] text-tx-muted',
            'hover:text-tx-primary hover:bg-bg-surface-2 transition-colors',
          )}
        >
          <Copy className="w-3 h-3" aria-hidden="true" />
          {t('decisions.detail.copy')}
        </button>
      </div>
      <pre
        data-testid="drawer-error-text"
        className={cn(
          'font-mono text-[12px] bg-bg-base rounded px-2.5 py-2',
          'whitespace-pre-wrap break-all select-text text-tx-secondary',
          'm-0',
        )}
      >
        {text}
      </pre>
    </section>
  );
}
