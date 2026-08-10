import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useDiscoveryRows } from '@/api/discoveryRows';
import { DiscoveryRail } from './DiscoveryRail';

// DiscoveryRails — fetches the effective row-config (GET /discovery/rows) and
// renders one DiscoveryRail per enabled row, in position order (BE already
// sorts). Replaces the fixed Tabs in DiscoveryPage.
export function DiscoveryRails() {
  const { t } = useTranslation();
  const q = useDiscoveryRows();

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

  const rows = (q.data?.rows ?? []).filter((r) => r.enabled);
  return (
    <div className="space-y-8" data-testid="discovery-rails">
      {rows.map((row) => (
        <DiscoveryRail key={row.id ?? `${row.row_type}-${row.position}`} row={row} />
      ))}
    </div>
  );
}
