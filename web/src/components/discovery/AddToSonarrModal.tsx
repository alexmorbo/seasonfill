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
import { useMe } from '@/hooks/useMe';
import { useInstances } from '@/lib/instances';
import { ApiError } from '@/lib/api';
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
    } else if (!instanceName) {
      const first = instances[0]?.name ?? '';
      if (first) setInstanceName(first);
    }
  }

  const qpQ = useQualityProfiles(instanceName, open && instanceName !== '');
  const rfQ = useRootFolders(instanceName, open && instanceName !== '');

  // Reset the profile + folder when the instance switches — the cached
  // choice is meaningless against a different Sonarr.
  const [prevInstance, setPrevInstance] = useState(instanceName);
  if (prevInstance !== instanceName) {
    setPrevInstance(instanceName);
    if (qualityProfileId) setQualityProfileId('');
    if (rootFolderPath) setRootFolderPath('');
  }

  const addMut = useAddToSonarr();

  const tagPreview = previewTag(me.data?.username);

  const tvdbID = item.tvdb_id;
  const canSubmit = Boolean(
    instanceName && qualityProfileId && rootFolderPath
    && typeof tvdbID === 'number' && tvdbID > 0
    && !addMut.isPending,
  );

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit || typeof tvdbID !== 'number') return;
    addMut.mutate(
      {
        instance_name: instanceName,
        tvdb_id: tvdbID,
        quality_profile_id: Number(qualityProfileId),
        root_folder_path: rootFolderPath,
        monitor_mode: monitorMode,
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
