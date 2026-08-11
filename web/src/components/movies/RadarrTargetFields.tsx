// RadarrTargetFields — the shared instance / quality-profile / root-folder /
// minimum-availability / search-on-add field cluster used by BOTH the
// AddToRadarrModal (single movie) and the MovieCollectionBlock add-all-missing
// dialog. Extracted to kill duplication of the per-instance QP/RF default
// seeding effect (ADR-0009 S8 pattern, ported from AddToSonarrModal).
//
// Fully controlled: the parent owns the raw field state and passes setters;
// this component runs the QP/RF metadata queries + the once-per-instance
// seeding effect (which writes back through the setters).

import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useQualityProfiles, useRootFolders } from '@/hooks/useInstanceMetadata';
import type { Instance } from '@/lib/instances';
import type { MinimumAvailability } from '@/api/addToRadarr';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

const MIN_AVAILABILITY: readonly MinimumAvailability[] = [
  'announced', 'inCinemas', 'released',
];

export interface RadarrTargetFieldsProps {
  readonly idPrefix: string;
  readonly radarrInstances: readonly Instance[];
  readonly effectiveInstance: string;
  readonly onInstanceChange: (next: string) => void;
  readonly qualityProfileId: string;
  readonly onQualityProfileChange: (next: string) => void;
  readonly rootFolderPath: string;
  readonly onRootFolderChange: (next: string) => void;
  readonly minimumAvailability: MinimumAvailability;
  readonly onMinimumAvailabilityChange: (next: MinimumAvailability) => void;
  readonly searchOnAdd: boolean;
  readonly onSearchOnAddChange: (next: boolean) => void;
}

export function RadarrTargetFields({
  idPrefix,
  radarrInstances,
  effectiveInstance,
  onInstanceChange,
  qualityProfileId,
  onQualityProfileChange,
  rootFolderPath,
  onRootFolderChange,
  minimumAvailability,
  onMinimumAvailabilityChange,
  searchOnAdd,
  onSearchOnAddChange,
}: RadarrTargetFieldsProps) {
  const { t } = useTranslation();

  const enabled = effectiveInstance !== '';
  const qpQ = useQualityProfiles(effectiveInstance, enabled);
  const rfQ = useRootFolders(effectiveInstance, enabled);

  // Per-instance, per-field seeding trackers (ADR-0009 S8). Each ref holds the
  // instance name whose default we've already applied; a refetch for the SAME
  // instance never re-seeds (anti-clobber of a manual choice), an instance
  // switch re-seeds cleanly.
  const seededQpForRef = useRef<string | null>(null);
  const seededRfForRef = useRef<string | null>(null);

  useEffect(() => {
    if (!effectiveInstance) return;
    const inst = radarrInstances.find((i) => i.name === effectiveInstance);

    if (seededQpForRef.current !== effectiveInstance && qpQ.data) {
      seededQpForRef.current = effectiveInstance;
      const def = inst?.default_quality_profile_id;
      if (
        typeof def === 'number'
        && (qpQ.data.items ?? []).some((qp) => qp.id === def)
      ) {
        onQualityProfileChange(String(def));
      }
    }

    if (seededRfForRef.current !== effectiveInstance && rfQ.data) {
      seededRfForRef.current = effectiveInstance;
      const def = inst?.default_root_folder_path;
      if (
        typeof def === 'string'
        && (rfQ.data.items ?? []).some((rf) => rf.path === def && rf.accessible)
      ) {
        onRootFolderChange(def);
      }
    }
  }, [
    effectiveInstance, radarrInstances, qpQ.data, rfQ.data,
    onQualityProfileChange, onRootFolderChange,
  ]);

  return (
    <>
      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-instance`}>{t('movies.add.instance')}</Label>
        {radarrInstances.length === 0 ? (
          <p
            data-testid="add-to-radarr-no-instances"
            className="text-sm text-tx-muted"
          >
            {t('movies.add.noRadarr')}
          </p>
        ) : (
          <Select value={effectiveInstance} onValueChange={onInstanceChange}>
            <SelectTrigger id={`${idPrefix}-instance`} data-testid="add-to-radarr-instance">
              <SelectValue placeholder={t('movies.add.instance')} />
            </SelectTrigger>
            <SelectContent>
              {radarrInstances.map((i) => (
                <SelectItem key={i.name} value={i.name ?? ''}>
                  {i.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-qp`}>{t('movies.add.qualityProfile')}</Label>
        <Select
          value={qualityProfileId}
          onValueChange={(v) => v && onQualityProfileChange(v)}
          disabled={!effectiveInstance || qpQ.isPending}
        >
          <SelectTrigger id={`${idPrefix}-qp`} data-testid="add-to-radarr-qp">
            <SelectValue placeholder={t('movies.add.qualityProfile')} />
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
        <Label htmlFor={`${idPrefix}-rf`}>{t('movies.add.rootFolder')}</Label>
        <Select
          value={rootFolderPath}
          onValueChange={(v) => v && onRootFolderChange(v)}
          disabled={!effectiveInstance || rfQ.isPending}
        >
          <SelectTrigger id={`${idPrefix}-rf`} data-testid="add-to-radarr-rf">
            <SelectValue placeholder={t('movies.add.rootFolder')} />
          </SelectTrigger>
          <SelectContent>
            {(rfQ.data?.items ?? []).map((rf) => (
              <SelectItem key={rf.id} value={rf.path} disabled={!rf.accessible}>
                {rf.path}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor={`${idPrefix}-minavail`}>{t('movies.add.minAvailability')}</Label>
        <Select
          value={minimumAvailability}
          onValueChange={(v) => v && onMinimumAvailabilityChange(v as MinimumAvailability)}
        >
          <SelectTrigger id={`${idPrefix}-minavail`} data-testid="add-to-radarr-minavail">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {MIN_AVAILABILITY.map((v) => (
              <SelectItem key={v} value={v}>
                {t(`movies.add.minAvail.${v}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <label
        className="flex items-center gap-2 text-sm"
        data-testid="add-to-radarr-search-on-add"
      >
        <Checkbox
          checked={searchOnAdd}
          onCheckedChange={(v) => onSearchOnAddChange(v === true)}
        />
        <span>{t('movies.add.searchOnAdd')}</span>
      </label>
    </>
  );
}
