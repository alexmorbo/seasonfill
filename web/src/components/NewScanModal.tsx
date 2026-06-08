import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm, useWatch } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import { SeriesPicker } from '@/components/SeriesPicker';
import { ApiError } from '@/lib/api';
import { useInstances, type Instance } from '@/lib/instances';
import { firstScanRunId, useTriggerScan } from '@/lib/scan-mutations';
import { KIND_DOT, healthKind, healthLabelKey } from '@/lib/badge-variants';
import { cn } from '@/lib/utils';

// 'default' = omit dry_run from payload (use instance config)
// 'on'      = force dry_run: true  (preview-only)
// 'off'     = force dry_run: false (real grab)
const DRY_RUN_CHOICES = ['default', 'on', 'off'] as const;
type DryRunChoice = (typeof DRY_RUN_CHOICES)[number];

const schema = z.object({
  instance: z.string().min(1, 'scans.newScanModal.selectInstance'),
  dry_run: z.enum(DRY_RUN_CHOICES),
});
export type NewScanFormValues = z.infer<typeof schema>;

function pickDefaultInstance(list: readonly Instance[]): string {
  const healthy = list.find((i) => i.name && healthKind(i.health) === 'success');
  if (healthy?.name) return healthy.name;
  return list[0]?.name ?? '';
}

export interface NewScanModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function NewScanModal({ open, onOpenChange }: NewScanModalProps) {
  const { t } = useTranslation();
  const { data } = useInstances();
  const instances = useMemo<readonly Instance[]>(
    () => data?.instances ?? [],
    [data],
  );
  const navigate = useNavigate();
  const trigger = useTriggerScan();

  const form = useForm<NewScanFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { instance: '', dry_run: 'default' },
  });

  // Picker selection — Q-013b-5 resets on instance change.
  const [seriesIds, setSeriesIds] = useState<ReadonlyArray<number>>([]);
  // Gate submit while picker debounces/fetches (prevents submitting
  // a half-typed query and getting an all-series scan by mistake).
  const [pickerLoading, setPickerLoading] = useState(false);

  // Re-seed the default when the dialog opens or the instance list arrives.
  useEffect(() => {
    if (!open) return;
    const current = form.getValues('instance');
    if (current && instances.some((i) => i.name === current)) return;
    const next = pickDefaultInstance(instances);
    if (next) form.setValue('instance', next, { shouldValidate: true });
  }, [open, instances, form]);

  const selectedName = useWatch({ control: form.control, name: 'instance' });
  const selected = instances.find((i) => i.name === selectedName);
  const degraded = selected && selected.health && healthKind(selected.health) !== 'success';

  // Q-013b-5: clear picker selection on instance change via the React
  // "adjust state during render" pattern instead of an effect — both
  // produce the same observable empty-on-switch behavior.
  const [prevSelectedName, setPrevSelectedName] = useState(selectedName);
  if (prevSelectedName !== selectedName) {
    setPrevSelectedName(selectedName);
    if (seriesIds.length > 0) setSeriesIds([]);
  }

  const dryChoice = useWatch({ control: form.control, name: 'dry_run' });

  const onSubmit = form.handleSubmit(async (values) => {
    try {
      const payload: {
        instance: string;
        series_ids?: readonly number[];
        dry_run?: boolean;
      } = { instance: values.instance };
      if (seriesIds.length > 0) payload.series_ids = seriesIds;
      if (values.dry_run === 'on')  payload.dry_run = true;
      if (values.dry_run === 'off') payload.dry_run = false;
      // 'default' → field omitted; backend uses per-instance config.

      const resp = await trigger.mutateAsync(payload);
      const id = firstScanRunId(resp);
      toast.success(
        seriesIds.length > 0
          ? t('scans.newScanModal.startedWithSeries', { instance: values.instance, count: seriesIds.length })
          : t('scans.newScanModal.started', { instance: values.instance }),
      );
      onOpenChange(false);
      form.reset({ instance: '', dry_run: 'default' });
      setSeriesIds([]);
      navigate(`/scans/${id}`);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 409) {
          toast.error(t('scans.newScanModal.alreadyInProgress', { instance: values.instance }));
        } else if (err.status === 404) {
          toast.error(t('scans.newScanModal.unknownInstance', { instance: values.instance }));
        } else {
          toast.error(t('scans.newScanModal.failed', { error: err.message }));
        }
      } else if (err instanceof Error) {
        toast.error(t('scans.newScanModal.failed', { error: err.message }));
      } else {
        toast.error(t('scans.newScanModal.failedGeneric'));
      }
    }
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[480px] p-0">
        <form onSubmit={onSubmit} aria-label={t('scans.newScanModal.formAria')}>
          <DialogHeader className="px-5 pt-5 pb-3 border-b border-border-faint">
            <DialogTitle>{t('scans.newScanModal.title')}</DialogTitle>
            <DialogDescription className="text-[12px] text-muted">
              {t('scans.newScanModal.subtitle')}
            </DialogDescription>
          </DialogHeader>

          <div className="px-5 py-4 flex flex-col gap-5">
            <fieldset className="flex flex-col gap-2">
              <Label className="text-[11px] uppercase tracking-[0.06em] text-faint">{t('scans.newScanModal.instance')}</Label>
              {instances.length === 0 ? (
                <p className="text-[12px] text-muted">{t('settings.instances.none')}</p>
              ) : (
                <RadioGroup
                  value={selectedName}
                  onValueChange={(v) => form.setValue('instance', v, { shouldValidate: true })}
                  className="flex flex-col gap-1.5"
                >
                  {instances.map((inst) => {
                    const name = inst.name ?? '';
                    if (!name) return null;
                    const checked = selectedName === name;
                    const dot = KIND_DOT[healthKind(inst.health)];
                    return (
                      <Label
                        key={name}
                        htmlFor={`inst-${name}`}
                        className={cn(
                          'flex items-center gap-3 px-3 py-2 rounded-md border cursor-pointer transition',
                          checked
                            ? 'border-accent/40 bg-surface-2'
                            : 'border-border-faint hover:bg-surface',
                        )}
                      >
                        <RadioGroupItem id={`inst-${name}`} value={name} />
                        <span className="font-mono text-[13px] flex-1">{name}</span>
                        <span className="inline-flex items-center gap-1.5 text-[11px] font-mono text-muted">
                          <span className={cn('inline-block w-1.5 h-1.5 rounded-full', dot)} />
                          {t(healthLabelKey(inst.health))}
                        </span>
                      </Label>
                    );
                  })}
                </RadioGroup>
              )}
              {form.formState.errors.instance && (
                <p className="text-[12px] text-status-danger">
                  {t(form.formState.errors.instance.message ?? '')}
                </p>
              )}
              {degraded && (
                <p className="text-[12px] text-status-warning">
                  {t('scans.newScanModal.degradedWarning', { name: selected?.name, health: t(healthLabelKey(selected?.health)) })}
                </p>
              )}
            </fieldset>

            <fieldset className="flex flex-col gap-1.5">
              <Label className="text-[11px] uppercase tracking-[0.06em] text-faint">
                {t('scans.newScanModal.seriesFilter')}
              </Label>
              <SeriesPicker
                instance={selectedName}
                value={seriesIds}
                onChange={setSeriesIds}
                onLoadingChange={setPickerLoading}
                disabled={!selectedName}
                placeholder={t('scans.newScanModal.seriesPlaceholder')}
                helperText={t('scans.newScanModal.seriesHelper')}
              />
            </fieldset>

            <fieldset className="flex flex-col gap-2">
              <Label className="text-[11px] uppercase tracking-[0.06em] text-faint">
                {t('scans.newScanModal.dryRunLabel')}
              </Label>
              <p className="text-[12px] text-muted -mt-1">
                {t('scans.newScanModal.dryRunHelp')}
              </p>
              <RadioGroup
                value={dryChoice}
                onValueChange={(v) =>
                  form.setValue('dry_run', v as DryRunChoice, { shouldValidate: true })
                }
                className="flex flex-col gap-1.5"
              >
                {DRY_RUN_CHOICES.map((choice) => {
                  const checked = dryChoice === choice;
                  return (
                    <Label
                      key={choice}
                      htmlFor={`new-scan-dry-${choice}`}
                      data-testid={`new-scan-dry-${choice}`}
                      className={cn(
                        'flex items-center gap-3 px-3 py-2 rounded-md border cursor-pointer transition',
                        checked
                          ? 'border-accent/40 bg-surface-2'
                          : 'border-border-faint hover:bg-surface',
                      )}
                    >
                      <RadioGroupItem id={`new-scan-dry-${choice}`} value={choice} />
                      <span className="text-[13px]">
                        {t(`scans.newScanModal.dryRunOptions.${choice}`)}
                      </span>
                    </Label>
                  );
                })}
              </RadioGroup>
            </fieldset>
          </div>

          <DialogFooter className="px-5 py-3 border-t border-border-faint">
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              type="submit"
              disabled={trigger.isPending || instances.length === 0 || pickerLoading}
              data-testid="new-scan-submit"
            >
              {pickerLoading
                ? t('scans.newScanModal.searching')
                : trigger.isPending ? t('scans.newScanModal.starting') : t('scans.newScanModal.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
