import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Pencil } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { useDiscoveryRows } from '@/api/discoveryRows';
import { DiscoveryRail } from './DiscoveryRail';
import { MovieDiscoveryRail } from '@/components/movies/MovieDiscoveryRail';
import { DiscoveryRowsEditor } from './DiscoveryRowsEditor';

// DiscoveryRails — fetches the effective row-config (GET /discovery/rows) and
// renders one DiscoveryRail per enabled row, in position order (BE already
// sorts). ADR-0017 S2: an edit toggle swaps the rails for the config editor
// (DnD reorder + enable/disable + add-row + save/reset).
export function DiscoveryRails() {
  const { t } = useTranslation();
  const q = useDiscoveryRows();
  const [editing, setEditing] = useState(false);

  if (q.isPending) {
    return (
      <div className="space-y-8" data-testid="discovery-rails-skeleton">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="space-y-2">
            <Skeleton className="h-4 w-40" />
            <div className="flex gap-3">
              {Array.from({ length: 6 }).map((__, j) => (
                <Skeleton key={j} className="aspect-[2/3] w-[124px] rounded-md" />
              ))}
            </div>
          </div>
        ))}
      </div>
    );
  }
  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-rails-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }

  const all = q.data?.rows ?? [];

  if (editing) {
    return (
      <DiscoveryRowsEditor
        key={q.dataUpdatedAt}
        initial={all}
        onExit={() => setEditing(false)}
      />
    );
  }

  const rows = all.filter((r) => r.enabled);
  return (
    <div className="space-y-6" data-testid="discovery-rails">
      <div className="flex justify-end">
        <Button
          variant="outline" size="sm"
          onClick={() => setEditing(true)}
          data-testid="discovery-edit-open"
        >
          <Pencil /> {t('discovery.edit.button')}
        </Button>
      </div>
      <div className="space-y-8">
        {rows.map((row) => {
          const key = row.id ?? `${row.row_type}-${row.position}`;
          return row.media_type === 'movie'
            ? <MovieDiscoveryRail key={key} row={row} />
            : <DiscoveryRail key={key} row={row} />;
        })}
      </div>
    </div>
  );
}
