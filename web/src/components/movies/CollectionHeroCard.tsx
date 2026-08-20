// CollectionHeroCard — compact movie-collection card for the hero-right
// `.sd-next-wrap` slot (the same slot `NextEpisodeCard` occupies for series,
// via `MediaHero`'s `heroExtras.nextCard`). Visual language mirrors
// `NextEpisodeCard`'s "glass" shell (white-on-dark-scrim, narrow column)
// verbatim — see `shellClass('glass')` there for the token source.
//
// Data/mutations are the SAME hooks `MovieCollectionBlock` uses
// (`useMovieCollection` / `useSetCollectionMonitor` / `useAddAllMissing`) —
// this card is a narrower rendering of the same collection-header fields
// (poster/name/monitor-toggle/add-all), not a new data dependency. It does
// NOT render the parts grid `MovieCollectionBlock` shows below the hero;
// `GET /collections/:id` already returns `parts` alongside the header
// fields in one response, so omitting the grid here adds no new endpoint
// usage (BL-4 constraint).

import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { MediaImage } from '@/components/MediaImage';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { RadarrTargetFields } from './RadarrTargetFields';
import { useCollectionCardState } from '@/hooks/useCollectionCardState';

export interface CollectionHeroCardProps {
  readonly tmdbCollectionId: number;
  /** Instance to query the collection with + the preferred monitor target
   *  (the movie's first library instance) — same contract as
   *  `MovieCollectionBlock`. */
  readonly instance?: string | undefined;
  /** BCP-47 tag forwarded as `?lang=` so the BE emits a localized name. */
  readonly lang?: string | undefined;
  readonly className?: string | undefined;
}

// Same glass-shell tokens as `NextEpisodeCard`'s `shellClass('glass')` —
// frosted dark scrim, white text, narrow column. Vertical (flex-col) layout
// here instead of NextEpisodeCard's horizontal badge+text row.
const heroCardClass = cn(
  'flex flex-col gap-2.5 rounded-lg p-3',
  'w-[240px] max-w-[30vw] text-white',
  'border border-white/[0.14] shadow-[0_12px_34px_oklch(0_0_0/.42)]',
  '[background:oklch(0.15_0.006_270/.60)]',
  '[backdrop-filter:blur(12px)] [-webkit-backdrop-filter:blur(12px)]',
);

export function CollectionHeroCard({
  tmdbCollectionId, instance, lang, className,
}: CollectionHeroCardProps) {
  const { t } = useTranslation();
  const {
    query, collection, monitored, monitorInstance, monitorMut, handleMonitorToggle,
    radarrInstances, dialogOpen, setDialogOpen, effectiveInstance,
    qualityProfileId, setQualityProfileId, rootFolderPath, setRootFolderPath,
    minimumAvailability, setMinimumAvailability, searchOnAdd, setSearchOnAdd,
    handleInstanceChange, canSubmitAddAll, handleAddAll, addAllMut,
  } = useCollectionCardState({ tmdbCollectionId, instance, lang });

  if (query.isPending) {
    return (
      <div
        data-testid="movie-collection-hero-loading"
        className={cn(heroCardClass, className)}
      >
        <div className="flex items-center gap-2.5">
          <Skeleton className="h-14 w-10 shrink-0 rounded-md" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-3.5 w-full" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        </div>
      </div>
    );
  }
  // A broken collection card must not break the hero.
  if (query.isError || !collection) return null;

  return (
    <div
      data-testid="movie-collection-hero-card"
      className={cn(heroCardClass, className)}
    >
      <div className="flex items-center gap-2.5">
        <div className="w-10 h-14 shrink-0 overflow-hidden rounded-md border border-white/15">
          <MediaImage
            hash={collection.poster ?? null}
            kind="poster"
            title={collection.name ?? ''}
            fallback="monogram"
            eager
            data-testid="movie-collection-hero-poster"
          />
        </div>
        <div className="flex flex-1 min-w-0 flex-col gap-1">
          <span
            data-testid="movie-collection-hero-name"
            className="text-[12.5px] font-semibold text-white truncate"
            title={collection.name}
          >
            {collection.name}
          </span>
          <label className="flex items-center gap-1.5 text-[11px] text-white/70">
            <Switch
              checked={monitored}
              disabled={monitored || !monitorInstance || monitorMut.isPending}
              onCheckedChange={handleMonitorToggle}
              className="scale-90"
              data-testid="movie-collection-hero-toggle"
            />
            <span className="truncate">{t('movieCollection.monitorToggle')}</span>
          </label>
        </div>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="w-full border-white/25 bg-white/[0.06] text-white hover:bg-white/[0.12] hover:text-white"
            data-testid="movie-collection-hero-add-all"
            disabled={radarrInstances.length === 0}
          >
            {t('movieCollection.addAll')}
          </Button>
        </DialogTrigger>
        <DialogContent data-testid="movie-collection-hero-add-all-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>{t('movieCollection.addAll')}</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleAddAll} className="space-y-4" data-testid="movie-collection-hero-add-all-form">
            <RadarrTargetFields
              idPrefix="chc"
              radarrInstances={radarrInstances}
              effectiveInstance={effectiveInstance}
              onInstanceChange={handleInstanceChange}
              qualityProfileId={qualityProfileId}
              onQualityProfileChange={setQualityProfileId}
              rootFolderPath={rootFolderPath}
              onRootFolderChange={setRootFolderPath}
              minimumAvailability={minimumAvailability}
              onMinimumAvailabilityChange={setMinimumAvailability}
              searchOnAdd={searchOnAdd}
              onSearchOnAddChange={setSearchOnAdd}
            />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
                data-testid="movie-collection-hero-add-all-cancel"
              >
                {t('movies.add.cancel')}
              </Button>
              <Button
                type="submit"
                disabled={!canSubmitAddAll}
                data-testid="movie-collection-hero-add-all-submit"
              >
                {addAllMut.isPending
                  ? t('movies.add.submitting')
                  : t('movieCollection.addAll')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
