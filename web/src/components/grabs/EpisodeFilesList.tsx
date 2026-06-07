import { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, AlertTriangle, ChevronDown } from 'lucide-react';
import { useGrabEpisodeFiles, type EpisodeFile } from '@/lib/api/grabEpisodeFiles';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { formatSize } from '@/lib/grabs/format';
import { cn } from '@/lib/utils';

const COLLAPSED_LIMIT = 5;

function fileBasename(p: string): string {
  const ix = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'));
  return ix >= 0 ? p.slice(ix + 1) : p;
}

function formatEpisodeLabel(seasonNumber: number, episodeNumbers: readonly number[]): string {
  const s = `S${String(seasonNumber).padStart(2, '0')}`;
  if (episodeNumbers.length === 0) return s;
  const sorted = [...episodeNumbers].sort((a, b) => a - b);
  const labels = sorted.map((n) => `E${String(n).padStart(2, '0')}`);
  return `${s}${labels.join('').replace(/^E/, 'E')}`;
}

export interface EpisodeFilesListProps {
  readonly instance: string | null;
  readonly grabId: string | null;
  readonly grabStatus: string | null | undefined;
  readonly open: boolean;
}

export function EpisodeFilesList({
  instance, grabId, grabStatus, open,
}: EpisodeFilesListProps) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const query = useGrabEpisodeFiles(instance, grabId, { enabled: open });

  const items = useMemo(
    () => (query.data?.items ?? []) as readonly EpisodeFile[],
    [query.data],
  );
  const shown = expanded ? items : items.slice(0, COLLAPSED_LIMIT);
  const hidden = items.length - shown.length;

  if (query.isPending && query.fetchStatus !== 'idle') {
    return (
      <div className="flex flex-col gap-1.5">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-3/4" />
      </div>
    );
  }

  if (query.isError) {
    return (
      <Alert variant="destructive">
        <AlertTriangle className="size-4" />
        <AlertTitle>{t('grabs.drawer.files.error.title')}</AlertTitle>
        <AlertDescription>
          {query.error.message}
          {' '}
          <Button variant="link" size="sm" onClick={() => query.refetch()}>
            {t('common.retry')}
          </Button>
        </AlertDescription>
      </Alert>
    );
  }

  if (items.length === 0) {
    const key = grabStatus === 'imported'
      ? 'grabs.drawer.files.emptyImported'
      : 'grabs.drawer.files.emptyNotImported';
    return (
      <p className="text-[12px] text-tx-faint italic">{t(key)}</p>
    );
  }

  return (
    <div className="flex flex-col gap-1">
      {shown.map((f) => (
        <div
          key={f.id}
          data-testid={`episode-file-${f.id}`}
          className={cn(
            'flex items-center gap-2 py-1.5 border-b border-border-faint last:border-b-0',
            'font-mono text-[11.5px] text-tx-muted',
          )}
        >
          <Check className="size-3 text-ok flex-none" />
          <span className="text-tx-secondary font-semibold flex-none">
            {formatEpisodeLabel(f.season_number, f.episode_numbers)}
          </span>
          <span className="truncate" title={f.relative_path}>
            {fileBasename(f.relative_path)}
          </span>
          <span className="ml-auto text-tx-faint flex-none">
            {formatSize(f.size_bytes)}
          </span>
        </div>
      ))}
      {hidden > 0 && (
        <button
          type="button"
          onClick={() => setExpanded(true)}
          className={cn(
            'flex items-center gap-1 text-[11.5px] font-mono text-tx-faint',
            'hover:text-tx-muted transition-colors py-1',
          )}
        >
          <ChevronDown className="size-3" />
          {t('grabs.drawer.files.more', { count: hidden })}
        </button>
      )}
    </div>
  );
}
