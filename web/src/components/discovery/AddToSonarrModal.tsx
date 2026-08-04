// S5 / ADR-0008: Add-to-Sonarr modal, rewritten on clean state. The modal is
// rendered by AddToSonarrProvider (app-shell level), never inside a card, so
// no interaction here can navigate the host surface. Lifecycle (open/close
// reset) is handled by provider mount/unmount; this component holds only its
// own transient field state and never mutates state during render.
//
// Wiring contract (unchanged from story 522/524):
//   - useInstances() populates the instance dropdown (all instances are valid
//     Sonarr targets today; no kind filter).
//   - Quality profile + root folder dropdowns are gated on a selected
//     instance (the BE 404s if asked before then).
//   - The "sf-{username}" badge previews the BE resolver; bypass/api-key/
//     local/anonymous collapse to "sf-system" server-side.
//   - Submit toasts on success/error. Errors map from the F-2c envelope's
//     `error` slug; unknown slugs fall back to the generic message. The
//     discovery cache is invalidated inside useAddToSonarr().

import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAddToSonarr } from '@/api/discovery';
import type { AddToSonarrTarget } from './add-to-sonarr-context';
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

const ERROR_SLUGS = new Set([
  'instance_not_found', 'sonarr_unreachable', 'invalid_request',
]);

export interface AddToSonarrModalProps {
  readonly target: AddToSonarrTarget;
  readonly onClose: () => void;
}

function previewTag(username: string | undefined): string {
  const u = (username ?? '').trim();
  if (u === '' || u === 'api-key' || u === 'local' || u === 'anonymous') {
    return 'sf-system';
  }
  return `sf-${u}`;
}

export function AddToSonarrModal({ target, onClose }: AddToSonarrModalProps) {
  const { t } = useTranslation();
  const me = useMe();
  const instancesQ = useInstances();
  const instances = useMemo(
    () => instancesQ.data?.instances?.filter((i) => Boolean(i.name)) ?? [],
    [instancesQ.data],
  );

  // User's explicit instance choice ('' = not yet chosen). The EFFECTIVE
  // instance auto-falls back to the first available one — derived, so no
  // render-phase setState is needed to "auto-pick".
  const [explicitInstance, setExplicitInstance] = useState(target.instanceName ?? '');
  const effectiveInstance = explicitInstance || (instances[0]?.name ?? '');

  const [qualityProfileId, setQualityProfileId] = useState('');
  const [rootFolderPath, setRootFolderPath] = useState('');
  const [searchOnAdd, setSearchOnAdd] = useState(true);
  // null = "untouched" → render the lookup-derived default selection.
  // Any explicit toggle snapshots the current effective set into a Set.
  const [seasonOverride, setSeasonOverride] = useState<Set<number> | null>(
    null,
  );

  // ADR-0009 S8: per-instance, per-field seeding trackers for the
  // quality-profile / root-folder defaults. Each ref holds the instance name
  // whose defaults we've already applied (independently per field). A metadata
  // refetch for the SAME instance never re-seeds (anti-clobber of a manual
  // choice); an instance switch (ref !== effectiveInstance) re-seeds cleanly.
  const seededQpForRef = useRef<string | null>(null);
  const seededRfForRef = useRef<string | null>(null);

  const enabled = effectiveInstance !== '';
  const qpQ = useQualityProfiles(effectiveInstance, enabled);
  const rfQ = useRootFolders(effectiveInstance, enabled);
  const lookupQ = useSonarrLookup(effectiveInstance, target.tvdbId, enabled);

  // ADR-0009 S8: seed the quality-profile / root-folder selects from the
  // chosen instance's list-DTO defaults. Gated per field on the corresponding
  // metadata query having returned AND the default being present in that fresh
  // list (root: present AND accessible). Soft-validate: a default absent from
  // the fresh list leaves the field empty, no error. Covers the derived
  // initial instance (instances[0]), which never flows through
  // handleInstanceChange. Seeds once per instance (seeded refs), so a refetch
  // for the same instance never clobbers a manual override.
  useEffect(() => {
    if (!effectiveInstance) return;
    const inst = instances.find((i) => i.name === effectiveInstance);

    if (seededQpForRef.current !== effectiveInstance && qpQ.data) {
      seededQpForRef.current = effectiveInstance;
      const def = inst?.default_quality_profile_id;
      if (
        typeof def === 'number'
        && (qpQ.data.items ?? []).some((qp) => qp.id === def)
      ) {
        // Post-commit seed of local, user-overridable state from async query
        // data; gated once-per-instance by the ref above (no loop, no clobber).
        // eslint-disable-next-line react-hooks/set-state-in-effect
        setQualityProfileId(String(def));
      }
    }

    if (seededRfForRef.current !== effectiveInstance && rfQ.data) {
      seededRfForRef.current = effectiveInstance;
      const def = inst?.default_root_folder_path;
      if (
        typeof def === 'string'
        && (rfQ.data.items ?? []).some(
          (rf) => rf.path === def && rf.accessible,
        )
      ) {
        // Post-commit seed (see the qp branch above); the effect-level
        // set-state-in-effect suppression there covers this branch too.
        setRootFolderPath(def);
      }
    }
  }, [effectiveInstance, instances, qpQ.data, rfQ.data]);

  function handleInstanceChange(next: string) {
    if (!next) return;
    // Instance switch resets instance-scoped fields (quality profile, root
    // folder). Season selection is portable across instances (season numbers
    // are stable), so it deliberately survives the switch.
    setExplicitInstance(next);
    setQualityProfileId('');
    setRootFolderPath('');
  }

  const lookupItems = lookupQ.data?.items;

  // Default selection: every regular season on, specials (0) off — mirrors
  // Sonarr's own UI. Derived from the lookup payload; no setState.
  const defaultSeasons = useMemo(() => {
    const s = new Set<number>();
    for (const it of lookupItems ?? []) {
      if (it.season_number > 0) s.add(it.season_number);
    }
    return s;
  }, [lookupItems]);

  const selectedSeasons = seasonOverride ?? defaultSeasons;

  const lookupNotFound =
    lookupQ.error instanceof ApiError && lookupQ.error.status === 404;
  const showSeasonsSection =
    Boolean(target.tvdbId) && enabled && !lookupNotFound;
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
    const base = new Set(selectedSeasons);
    if (checked) base.add(n);
    else base.delete(n);
    setSeasonOverride(base);
  }

  function toggleAll(checked: boolean) {
    if (!checked) {
      setSeasonOverride(new Set());
      return;
    }
    const next = new Set<number>();
    for (const s of sortedSeasons) next.add(s.season_number);
    setSeasonOverride(next);
  }

  const addMut = useAddToSonarr();
  const tagPreview = previewTag(me.data?.username);
  const tvdbID = target.tvdbId;

  const canSubmit = Boolean(
    effectiveInstance && qualityProfileId && rootFolderPath
    && typeof tvdbID === 'number' && tvdbID > 0
    && !addMut.isPending
    && !seasonsLoading,
  );

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit || typeof tvdbID !== 'number') return;
    const seasonsArr = Array.from(selectedSeasons).sort((a, b) => a - b);
    const includeSeasons = showSeasonsSection && !seasonsError
      && seasonsArr.length > 0;
    addMut.mutate(
      {
        instance_name: effectiveInstance,
        tvdb_id: tvdbID,
        quality_profile_id: Number(qualityProfileId),
        root_folder_path: rootFolderPath,
        search_on_add: searchOnAdd,
        ...(includeSeasons ? { monitored_seasons: seasonsArr } : {}),
      },
      {
        onSuccess: (res) => {
          toast.success(t('discovery.add.success', {
            tag: res.user_tag_label,
          }));
          onClose();
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
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="add-to-sonarr-modal" className="max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('discovery.add.modal_title', { title: target.title })}
          </DialogTitle>
          <DialogDescription>
            {t('discovery.add.tag_badge', { tag: tagPreview })}
          </DialogDescription>
        </DialogHeader>

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
                value={effectiveInstance}
                onValueChange={handleInstanceChange}
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
              disabled={!effectiveInstance || qpQ.isPending}
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
              disabled={!effectiveInstance || rfQ.isPending}
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
              {!seasonsLoading && !seasonsError && sortedSeasons.length > 0 && (
                <p
                  className="text-sm text-tx-muted"
                  data-testid="add-to-sonarr-seasons-hint"
                >
                  {t('discovery.add.seasons.monitor_hint')}
                </p>
              )}
            </div>
          )}

          <label
            className="flex items-center gap-2 text-sm"
            data-testid="add-to-sonarr-search-on-add"
          >
            <Checkbox
              checked={searchOnAdd}
              onCheckedChange={(v) => setSearchOnAdd(v === true)}
            />
            <span>{t('discovery.add.search_on_add')}</span>
          </label>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onClose()}
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
