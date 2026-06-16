import { useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { TorrentRow } from './TorrentRow';
import type { TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';

type SortKey = 'name' | 'added_on' | 'size' | 'progress' | 'ratio';
type SortDir = 'asc' | 'desc';

export interface TorrentsTableProps {
  readonly rows: readonly TorrentRowDTO[];
  readonly className?: string | undefined;
}

function compare(a: TorrentRowDTO, b: TorrentRowDTO, key: SortKey): number {
  switch (key) {
    case 'name':     return (a.name ?? '').localeCompare(b.name ?? '');
    case 'added_on': return new Date(a.added_on ?? 0).getTime() - new Date(b.added_on ?? 0).getTime();
    case 'size':     return (a.size_bytes ?? 0) - (b.size_bytes ?? 0);
    case 'progress': return (a.progress ?? 0) - (b.progress ?? 0);
    case 'ratio':    return (a.ratio ?? 0) - (b.ratio ?? 0);
  }
}

export function TorrentsTable({ rows, className }: TorrentsTableProps) {
  const { t } = useTranslation();
  const [key, setKey] = useState<SortKey>('added_on');
  const [dir, setDir] = useState<SortDir>('desc');

  const sorted = useMemo(() => {
    const out = rows.slice();
    out.sort((a, b) => (dir === 'desc' ? -1 : 1) * compare(a, b, key));
    return out;
  }, [rows, key, dir]);

  function onSort(next: SortKey) {
    if (next === key) {
      setDir((d) => (d === 'desc' ? 'asc' : 'desc'));
    } else {
      setKey(next);
      // default direction per column: numeric desc; name asc.
      setDir(next === 'name' ? 'asc' : 'desc');
    }
  }

  return (
    <div data-testid="torrents-table" className={cn('@container flex flex-col gap-1', className)}>
      <div
        className={cn(
          'hidden md:grid items-center gap-3 px-3 pb-1 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint',
          'grid-cols-[minmax(0,1fr)_auto_auto_140px_auto_auto_auto_auto_auto]',
          '@max-[1280px]:grid-cols-[minmax(0,1fr)_auto_auto_140px_auto_auto_auto_auto]',
          '@max-[1024px]:grid-cols-[minmax(0,1fr)_auto_auto_120px_auto]',
        )}
      >
        <SortHeader label={t('seriesDetail.torrents.col.name')}     active={key === 'name'}     dir={dir} onClick={() => onSort('name')} />
        <SortHeader label={t('seriesDetail.torrents.col.added')}    active={key === 'added_on'} dir={dir} onClick={() => onSort('added_on')} />
        <SortHeader label={t('seriesDetail.torrents.col.size')}     active={key === 'size'}     dir={dir} onClick={() => onSort('size')} />
        <SortHeader label={t('seriesDetail.torrents.col.progress')} active={key === 'progress'} dir={dir} onClick={() => onSort('progress')} />
        <span>{t('seriesDetail.torrents.col.state')}</span>
        <span className="@max-[1024px]:hidden">{t('seriesDetail.torrents.col.peers')}</span>
        <span className="@max-[1024px]:hidden">{t('seriesDetail.torrents.col.speed')}</span>
        <span className="@max-[1024px]:hidden">{t('seriesDetail.torrents.col.eta')}</span>
        <span className="@max-[1280px]:hidden">
          <SortHeader label={t('seriesDetail.torrents.col.ratio')} active={key === 'ratio'} dir={dir} onClick={() => onSort('ratio')} inline />
        </span>
      </div>

      <div className="flex flex-col gap-1">
        {sorted.map((row) => (
          <TorrentRow key={row.hash ?? `${row.name}-${row.added_on}`} row={row} />
        ))}
      </div>
    </div>
  );
}

function SortHeader({
  label, active, dir, onClick, inline,
}: { label: string; active: boolean; dir: SortDir; onClick: () => void; inline?: boolean }) {
  const Icon = !active ? ChevronsUpDown : dir === 'desc' ? ArrowDown : ArrowUp;
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={`sort-${label}`}
      className={cn(
        'inline-flex items-center gap-1 text-left',
        active ? 'text-tx-secondary' : 'text-tx-faint hover:text-tx-secondary',
        inline ? 'p-0' : 'p-0',
      )}
    >
      <span>{label}</span>
      <Icon className="w-3 h-3" aria-hidden="true" />
    </button>
  );
}
