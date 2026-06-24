// Story 522 / N-4e: modal that drives POST /api/v1/discovery/add-to-sonarr.
//
// Wiring contract:
//   - `useInstances()` populates the instance dropdown. Seasonfill only
//     supports Sonarr today, so every instance in the list is a valid
//     target — no kind filter needed.
//   - Quality profile + root folder dropdowns are gated on a selected
//     instance (the BE returns 404 if asked before then).
//   - The "Will be tagged as sf-{username}" badge previews the BE's
//     resolver. Bypass / api-key / local / anonymous all collapse to
//     "sf-system" server-side (see add_to_sonarr_handler.go line 102).
//   - Submit toasts on success and on error. Errors are mapped from the
//     F-2c envelope's `error` slug (`instance_not_found`,
//     `sonarr_unreachable`, `invalid_request`); anything else falls back
//     to the generic message. The discovery cache is invalidated inside
//     `useAddToSonarr()` so cards refetch their `in_library_instances`.

import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import {
  type AddToSonarrMonitorMode,
  type DiscoverySeriesItem,
  useAddToSonarr,
} from '@/api/discovery';
import { useQualityProfiles, useRootFolders } from '@/hooks/useInstanceMetadata';
import { useSonarrLookup } from '@/hooks/useSonarrLookup';
import { useMe } from '@/hooks/useMe';
import { useInstances } from '@/lib/instances';
import { ApiError } from '@/lib/api';
import { Checkbox } from '@/components/ui/checkbox';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

const MONITOR_MODES: readonly AddToSonarrMonitorMode[] = [
  'all', 'future', 'missing', 'none',
];

const ERROR_SLUGS = new Set([
  'instance_not_found', 'sonarr_unreachable', 'invalid_request',
]);

export interface AddToSonarrModalProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly item: DiscoverySeriesItem;
}

function previewTag(username: string | undefined): string {
  const u = (username ?? '').trim();
  if (u === '' || u === 'api-key' || u === 'local' || u === 'anonymous') {
    return 'sf-system';
  }
  return `sf-${u}`;
}

export function AddToSonarrModal({
  open, onOpenChange, item,
}: AddToSonarrModalProps) {
  const { t } = useTranslation();
  const me = useMe();
  const instancesQ = useInstances();
  const instances = useMemo(
    () => instancesQ.data?.instances?.filter((i) => Boolean(i.name)) ?? [],
    [instancesQ.data],
  );

  const [instanceName, setInstanceName] = useState('');
  const [qualityProfileId, setQualityProfileId] = useState<string>('');
  const [rootFolderPath, setRootFolderPath] = useState<string>('');
  const [monitorMode, setMonitorMode] =
    useState<AddToSonarrMonitorMode>('all');
  const [selectedSeasons, setSelectedSeasons] = useState<Set<number>>(
    new Set(),
  );
  const [seasonsInitialized, setSeasonsInitialized] = useState(false);

  // "Adjust state during render" — React's preferred shape for both
  // the open/close lifecycle reset AND the instance-change reset.
  // The lint rule `react-hooks/set-state-in-effect` (React 19) explicitly
  // pushes derived-state mutations out of effects; this branch is
  // cheaper and avoids the cascading-render warning.
  const [wasOpen, setWasOpen] = useState(open);
  if (wasOpen !== open) {
    setWasOpen(open);
    if (!open) {
      setInstanceName('');
      setQualityProfileId('');
      setRootFolderPath('');
      setMonitorMode('all');
      setSelectedSeasons(new Set());
      setSeasonsInitialized(false);
    } else if (!instanceName) {
      const first = instances[0]?.name ?? '';
      if (first) setInstanceName(first);
    }
  }

  const qpQ = useQualityProfiles(instanceName, open && instanceName !== '');
  const rfQ = useRootFolders(instanceName, open && instanceName !== '');
  const lookupQ = useSonarrLookup(
    instanceName,
    item.tvdb_id,
    open && instanceName !== '',
  );

  // Auto-pick the first instance once the list resolves. Without this
  // branch the modal sits at "Select instance" forever in the (rare)
  // case the dropdown loads after the dialog opens — pretty common in
  // tests + slow connections.
  if (open && instanceName === '' && instances.length > 0) {
    const first = instances[0]?.name ?? '';
    if (first) setInstanceName(first);
  }

  // Reset the profile + folder when the instance switches — the cached
  // choice is meaningless against a different Sonarr.
  const [prevInstance, setPrevInstance] = useState(instanceName);
  if (prevInstance !== instanceName) {
    setPrevInstance(instanceName);
    if (qualityProfileId) setQualityProfileId('');
    if (rootFolderPath) setRootFolderPath('');
    setSelectedSeasons(new Set());
    setSeasonsInitialized(false);
  }

  // Seed the season selection from the lookup payload once. Specials
  // (season 0) start unchecked — operators rarely want them on by
  // default and Sonarr's own UI mirrors this convention.
  const lookupItems = lookupQ.data?.items;
  if (lookupItems && !seasonsInitialized) {
    setSeasonsInitialized(true);
    const next = new Set<number>();
    for (const s of lookupItems) {
      if (s.season_number > 0) next.add(s.season_number);
    }
    setSelectedSeasons(next);
  }

  // 404 (series not found in Sonarr's lookup DB) — drop the section
  // and let the BE fall back to monitor_mode semantics. Any other
  // error (500/502/network) we surface with an inline message.
  const lookupNotFound =
    lookupQ.error instanceof ApiError && lookupQ.error.status === 404;
  const showSeasonsSection =
    Boolean(item.tvdb_id) && instanceName !== '' && !lookupNotFound;
  const seasonsLoading = showSeasonsSection && lookupQ.isPending;
  const seasonsError =
    showSeasonsSection && lookupQ.isError && !lookupNotFound;

  const sortedSeasons = useMemo(() => {
    const items = lookupItems ?? [];
    const regular = items
      .filter((s) => s.season_number > 0)
      .slice()
      .sort((a, b) => a.season_number - b.season_number);
    const specials = items.filter((s) => s.season_number === 0);
    return [...regular, ...specials];
  }, [lookupItems]);

  const allChecked =
    sortedSeasons.length > 0
    && sortedSeasons.every((s) => selectedSeasons.has(s.season_number));

  function toggleSeason(n: number, checked: boolean) {
    const next = new Set(selectedSeasons);
    if (checked) next.add(n);
    else next.delete(n);
    setSelectedSeasons(next);
  }

  function toggleAll(checked: boolean) {
    if (!checked) {
      setSelectedSeasons(new Set());
      return;
    }
    const next = new Set<number>();
    for (const s of sortedSeasons) next.add(s.season_number);
    setSelectedSeasons(next);
  }

  const addMut = useAddToSonarr();

  const tagPreview = previewTag(me.data?.username);

  const tvdbID = item.tvdb_id;
  const canSubmit = Boolean(
    instanceName && qualityProfileId && rootFolderPath
    && typeof tvdbID === 'number' && tvdbID > 0
    && !addMut.isPending
    && !seasonsLoading,
  );

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit || typeof tvdbID !== 'number') return;
    const seasonsArr = Array.from(selectedSeasons).sort((a, b) => a - b);
    const includeSeasons = showSeasonsSection && !seasonsError
      && seasonsArr.length > 0;
    addMut.mutate(
      {
        instance_name: instanceName,
        tvdb_id: tvdbID,
        quality_profile_id: Number(qualityProfileId),
        root_folder_path: rootFolderPath,
        monitor_mode: monitorMode,
        ...(includeSeasons ? { monitored_seasons: seasonsArr } : {}),
      },
      {
        onSuccess: (res) => {
          toast.success(t('discovery.add.success', {
            tag: res.user_tag_label,
          }));
          onOpenChange(false);
        },
        onError: (err) => {
          let key = 'discovery.add.errors.unknown';
          if (err instanceof ApiError) {
            const body = err.body as { error?: string } | undefined;
            const slug = body?.error;
            if (slug && ERROR_SLUGS.has(slug)) {
              key = `discovery.add.errors.${slug}`;
            }
          }
          toast.error(t(key));
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        data-testid="add-to-sonarr-modal"
        className="max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <DialogHeader>
          <DialogTitle>
            {t('discovery.add.modal_title', { title: item.title })}
          </DialogTitle>
          <DialogDescription>
            {t('discovery.add.tag_badge', { tag: tagPreview })}
          </DialogDescription>
        </DialogHeader>

        {/* Story 523 / N-4 unblock: the BE projects tvdb_id straight off
            the discovery list response, so the happy path always has it.
            Legacy stubs upserted before story 523's join landed may still
            be missing it — surface a non-fatal info banner so the user
            knows why Submit is disabled. The enrichment worker hydrates
            the field on the next /series/{id} pass. */}
        {(typeof tvdbID !== 'number' || tvdbID <= 0) && (
          <p
            data-testid="add-to-sonarr-missing-tvdb"
            className="text-sm text-tx-muted"
          >
            {t('discovery.add.missing_tvdb_id')}
          </p>
        )}

        <form
          onSubmit={handleSubmit}
          className="space-y-4"
          data-testid="add-to-sonarr-form"
        >
          <div className="space-y-1.5">
            <Label htmlFor="ats-instance">{t('discovery.add.instance')}</Label>
            {instances.length === 0 ? (
              <p
                data-testid="add-to-sonarr-no-instances"
                className="text-sm text-tx-muted"
              >
                {t('discovery.add.no_instances')}
              </p>
            ) : (
              <Select
                value={instanceName}
                onValueChange={(v) => v && setInstanceName(v)}
              >
                <SelectTrigger
                  id="ats-instance"
                  data-testid="add-to-sonarr-instance"
                >
                  <SelectValue
                    placeholder={t('discovery.add.instance_placeholder')}
                  />
                </SelectTrigger>
                <SelectContent>
                  {instances.map((i) => (
                    <SelectItem key={i.name} value={i.name ?? ''}>
                      {i.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="ats-qp">{t('discovery.add.quality_profile')}</Label>
            <Select
              value={qualityProfileId}
              onValueChange={(v) => v && setQualityProfileId(v)}
              disabled={!instanceName || qpQ.isPending}
            >
              <SelectTrigger id="ats-qp" data-testid="add-to-sonarr-qp">
                <SelectValue
                  placeholder={qpQ.isPending
                    ? t('discovery.add.quality_profile_loading')
                    : t('discovery.add.quality_profile_placeholder')}
                />
              </SelectTrigger>
              <SelectContent>
                {(qpQ.data?.items ?? []).map((qp) => (
                  <SelectItem key={qp.id} value={String(qp.id)}>
                    {qp.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="ats-rf">{t('discovery.add.root_folder')}</Label>
            <Select
              value={rootFolderPath}
              onValueChange={(v) => v && setRootFolderPath(v)}
              disabled={!instanceName || rfQ.isPending}
            >
              <SelectTrigger id="ats-rf" data-testid="add-to-sonarr-rf">
                <SelectValue
                  placeholder={rfQ.isPending
                    ? t('discovery.add.root_folder_loading')
                    : t('discovery.add.root_folder_placeholder')}
                />
              </SelectTrigger>
              <SelectContent>
                {(rfQ.data?.items ?? []).map((rf) => (
                  <SelectItem
                    key={rf.id}
                    value={rf.path}
                    disabled={!rf.accessible}
                  >
                    {rf.path}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {showSeasonsSection && (
            <div className="space-y-2" data-testid="add-to-sonarr-seasons">
              <Label>{t('discovery.add.seasons.label')}</Label>
              {seasonsLoading && (
                <p
                  className="text-sm text-tx-muted"
                  data-testid="add-to-sonarr-seasons-loading"
                >
                  {t('discovery.add.seasons.loading')}
                </p>
              )}
              {seasonsError && (
                <p
                  className="text-sm text-red-500"
                  data-testid="add-to-sonarr-seasons-error"
                >
                  {t('discovery.add.seasons.error')}
                </p>
              )}
              {!seasonsLoading && !seasonsError && sortedSeasons.length > 0 && (
                <div className="space-y-1.5">
                  <label
                    className="flex items-center gap-2 text-sm font-medium"
                    data-testid="add-to-sonarr-seasons-all"
                  >
                    <Checkbox
                      checked={allChecked}
                      onCheckedChange={(v) => toggleAll(v === true)}
                    />
                    <span>{t('discovery.add.seasons.all')}</span>
                  </label>
                  <div className="space-y-1 max-h-40 overflow-y-auto pr-1">
                    {sortedSeasons.map((s) => {
                      const checked = selectedSeasons.has(s.season_number);
                      const label = s.season_number === 0
                        ? t('discovery.add.seasons.specials')
                        : t('discovery.add.seasons.season_n',
                          { n: s.season_number });
                      return (
                        <label
                          key={s.season_number}
                          className="flex items-center gap-2 text-sm"
                          data-testid={`add-to-sonarr-season-${s.season_number}`}
                        >
                          <Checkbox
                            checked={checked}
                            onCheckedChange={(v) =>
                              toggleSeason(s.season_number, v === true)}
                          />
                          <span>{label}</span>
                          <span className="text-tx-muted">
                            ({t('discovery.add.seasons.episodes_count',
                              { count: s.episode_count })})
                          </span>
                        </label>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="ats-monitor">{t('discovery.add.monitor')}</Label>
            <Select
              value={monitorMode}
              onValueChange={(v) =>
                v && setMonitorMode(v as AddToSonarrMonitorMode)}
            >
              <SelectTrigger
                id="ats-monitor"
                data-testid="add-to-sonarr-monitor"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MONITOR_MODES.map((m) => (
                  <SelectItem key={m} value={m}>
                    {t(`discovery.add.monitor_options.${m}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              data-testid="add-to-sonarr-cancel"
            >
              {t('discovery.add.cancel')}
            </Button>
            <Button
              type="submit"
              disabled={!canSubmit}
              data-testid="add-to-sonarr-submit"
            >
              {addMut.isPending
                ? t('discovery.add.submitting')
                : t('discovery.add.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
