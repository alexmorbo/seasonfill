import { FolderInput, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface ProvenanceChipProps {
  readonly provenance: string | undefined;
  readonly className?: string | undefined;
}

// KNOWN_PROVENANCE mirrors dto.TorrentRow.Provenance's two documented
// values (radarr_search | manual_import — schema.ts docstring, B1.4
// handler). Any other/unrecognized value still renders defensively
// (against a future BE addition) using the raw string as its own label.
const KNOWN_PROVENANCE = new Set(['radarr_search', 'manual_import']);

// ProvenanceChip — B1.5 (ADR-0023). Surfaces dto.TorrentRow.provenance
// ("how did this download come to exist"). Provenance is MOVIE ROWS ONLY
// (schema.ts: "torrent_series_map has no provenance column, so this field
// is absent from every /series/:id/torrents payload (omitempty)") — so
// `provenance` is always `undefined` for series rows and this component
// renders nothing there. That's what makes mounting it from the SHARED
// TorrentRow.tsx / TorrentCard.tsx leaf components safe: zero behavior
// change for the series torrents panel.
export function ProvenanceChip({ provenance, className }: ProvenanceChipProps) {
  const { t } = useTranslation();
  if (!provenance) return null;
  const known = KNOWN_PROVENANCE.has(provenance);
  const Icon = provenance === 'manual_import' ? FolderInput : Search;
  const label = known
    ? t(`movieDetail.torrents.provenance.${provenance === 'manual_import' ? 'manualImport' : 'radarrSearch'}`)
    : provenance;
  return (
    <span
      data-testid="torrent-provenance"
      data-provenance={provenance}
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-medium',
        'bg-bg-surface-2 text-tx-muted border border-border-faint',
        className,
      )}
    >
      <Icon className="w-3 h-3" aria-hidden="true" />
      <span>{label}</span>
    </span>
  );
}
