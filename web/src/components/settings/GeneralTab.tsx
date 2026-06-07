import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import cronstrue from 'cronstrue';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { AlertTriangle, Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  useRuntimeConfig, useUpdateRuntimeConfig, type RuntimeConfig,
} from '@/lib/runtime-config';

// Go duration regex / parser — unchanged from previous impl.
const goDurRE = /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/;
function parseGoDurationMs(s: string): number | null {
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let total = 0, matched = false, cursor = 0;
  for (const m of s.matchAll(re)) {
    matched = true;
    if (m.index !== cursor) return null;
    cursor = m.index + m[0].length;
    const n = Number(m[1]);
    if (!Number.isFinite(n)) return null;
    switch (m[2]) {
      case 'ns': total += n / 1e6; break;
      case 'us': case 'µs': total += n / 1e3; break;
      case 'ms': total += n; break;
      case 's':  total += n * 1000; break;
      case 'm':  total += n * 60_000; break;
      case 'h':  total += n * 3_600_000; break;
      default: return null;
    }
  }
  if (!matched || cursor !== s.length) return null;
  return total;
}

const cronJitterMaxMs = 60 * 60 * 1000;
const scanShutdownGraceMinMs = 1000;
const scanShutdownGraceMaxMs = 10 * 60 * 1000;
const scanCooldownSweepMinMs = 10 * 1000;
const scanCooldownSweepMaxMs = 24 * 60 * 60 * 1000;

const schema = z.object({
  cron_enabled: z.boolean(),
  cron_schedule: z.string().min(1, 'settings.general.schedule.expressionRequired'),
  cron_on_start: z.boolean(),
  cron_jitter: z.string().regex(goDurRE, 'settings.general.schedule.jitterError')
    .refine((v) => {
      const ms = parseGoDurationMs(v);
      return ms !== null && ms >= 0 && ms <= cronJitterMaxMs;
    }, 'settings.general.schedule.jitterRange'),
  scan_shutdown_grace: z.string().regex(goDurRE, 'settings.general.scan.useGoDuration')
    .refine((v) => {
      const ms = parseGoDurationMs(v);
      return ms !== null && ms >= scanShutdownGraceMinMs && ms <= scanShutdownGraceMaxMs;
    }, 'settings.general.scan.shutdownGraceRange'),
  scan_cooldown_sweep: z.string().regex(goDurRE, 'settings.general.scan.useGoDuration')
    .refine((v) => {
      const ms = parseGoDurationMs(v);
      return ms !== null && ms >= scanCooldownSweepMinMs && ms <= scanCooldownSweepMaxMs;
    }, 'settings.general.scan.cooldownSweepRange'),
  dry_run: z.boolean(),
  global_rpm: z.number().int().min(0, 'settings.general.defaults.rpmRange').max(10000, 'settings.general.defaults.rpmMax'),
  global_burst: z.number().int().min(0, 'settings.general.defaults.rpmRange').max(10000, 'settings.general.defaults.rpmMax'),
});
type FormValues = z.infer<typeof schema>;

function configToForm(c: RuntimeConfig | undefined): FormValues {
  return {
    cron_enabled: Boolean(c?.cron?.enabled ?? true),
    cron_schedule: c?.cron?.schedule ?? '0 */6 * * *',
    cron_on_start: Boolean(c?.cron?.on_start ?? false),
    cron_jitter: c?.cron?.jitter ?? '1m',
    scan_shutdown_grace: c?.scan?.shutdown_grace ?? '60s',
    scan_cooldown_sweep: c?.scan?.cooldown_sweep ?? '15m',
    dry_run: Boolean(c?.dry_run ?? true),
    global_rpm: c?.global_rate_limit?.rpm ?? 30,
    global_burst: c?.global_rate_limit?.burst ?? 10,
  };
}

function formToPayload(prev: Partial<RuntimeConfig> | undefined, v: FormValues): RuntimeConfig {
  const base = prev ?? {};
  return {
    ...base,
    cron: { enabled: v.cron_enabled, schedule: v.cron_schedule, on_start: v.cron_on_start, jitter: v.cron_jitter },
    scan: { shutdown_grace: v.scan_shutdown_grace, cooldown_sweep: v.scan_cooldown_sweep },
    dry_run: v.dry_run,
    global_rate_limit: { rpm: v.global_rpm, burst: v.global_burst },
  } as RuntimeConfig;
}

function describeCron(expr: string, fallback: string): { ok: boolean; text: string } {
  try { return { ok: true, text: cronstrue.toString(expr, { throwExceptionOnParseError: true }) }; }
  catch { return { ok: false, text: fallback }; }
}

// .set-block card wrapper matching the design's `.set-block` + `.bh` pattern.
function Block({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3.5">
      <header className="flex flex-col gap-[3px]">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">{title}</h2>
        {subtitle && <p className="text-[12.5px] text-muted m-0">{subtitle}</p>}
      </header>
      {children}
    </section>
  );
}

function FieldRow({ htmlFor, label, hint, control }: { htmlFor?: string; label: string; hint?: string; control: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3.5 py-[11px] border-b border-border-faint last:border-b-0">
      <div className="flex flex-col">
        <Label htmlFor={htmlFor} className="text-[13.5px] font-[550]">{label}</Label>
        {hint && <span className="text-[12px] text-muted">{hint}</span>}
      </div>
      {control}
    </div>
  );
}

export function GeneralTab() {
  const { t } = useTranslation();
  const q = useRuntimeConfig();
  const mut = useUpdateRuntimeConfig();

  const {
    register, handleSubmit, reset, watch, setValue,
    formState: { errors, isDirty, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: configToForm(undefined),
    mode: 'onBlur',
  });

  useEffect(() => {
    if (q.data?.config && !isDirty) reset(configToForm(q.data.config));
  }, [q.data?.config, isDirty, reset]);

  const cronVal = watch('cron_schedule');
  const cronInvalidLabel = t('settings.general.schedule.invalidExpression');
  const cronPreview = useMemo(
    () => describeCron(cronVal, cronInvalidLabel),
    [cronVal, cronInvalidLabel],
  );

  const onSubmit = handleSubmit((values) => {
    mut.mutate(formToPayload(q.data?.config, values), {
      onSuccess: (data) => reset(configToForm(data.config)),
    });
  });
  const onDiscard = () => reset(configToForm(q.data?.config));

  if (q.isPending) {
    return (
      <div className="flex items-center gap-2 text-muted text-[13px]">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> {t('common.loadingSettings')}
      </div>
    );
  }
  if (q.isError) {
    return (
      <Alert variant="destructive">
        <AlertTriangle className="w-4 h-4" />
        <AlertTitle>{t('settings.loadFailed')}</AlertTitle>
        <AlertDescription>{q.error.message}</AlertDescription>
      </Alert>
    );
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-5" noValidate>
      <Block
        title={t('settings.general.schedule.section')}
        subtitle={t('settings.general.schedule.blockSubtitle', { defaultValue: '' })}
      >
        <div className="grid grid-cols-2 gap-3.5">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cron-schedule">{t('settings.general.schedule.expression')}</Label>
            <Input
              id="cron-schedule" placeholder="0 */6 * * *"
              className="font-mono"
              aria-invalid={Boolean(errors.cron_schedule) || undefined}
              {...register('cron_schedule')}
            />
            <span
              className={cn('text-[11.5px]', cronPreview.ok ? 'text-tx-faint' : 'text-status-danger')}
              role={cronPreview.ok ? undefined : 'alert'}
            >
              {cronPreview.text}
            </span>
            {errors.cron_schedule && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.cron_schedule.message ?? '')}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cron-jitter">{t('settings.general.schedule.jitter')}</Label>
            <Input
              id="cron-jitter" placeholder="1m" className="font-mono"
              aria-invalid={Boolean(errors.cron_jitter) || undefined}
              {...register('cron_jitter')}
            />
            {errors.cron_jitter && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.cron_jitter.message ?? '')}
              </p>
            )}
          </div>
        </div>
        <FieldRow
          htmlFor="cron-enabled"
          label={t('settings.general.schedule.enabled')}
          hint={t('settings.general.schedule.enabledHint')}
          control={
            <Switch
              id="cron-enabled" checked={watch('cron_enabled')}
              onCheckedChange={(v) => setValue('cron_enabled', v, { shouldDirty: true })}
            />
          }
        />
        <FieldRow
          htmlFor="cron-on-start"
          label={t('settings.general.schedule.onStart')}
          control={
            <Switch
              id="cron-on-start" checked={watch('cron_on_start')}
              onCheckedChange={(v) => setValue('cron_on_start', v, { shouldDirty: true })}
            />
          }
        />
      </Block>

      <Block title={t('settings.general.scan.section')}>
        <div className="grid grid-cols-2 gap-3.5">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="scan-sweep">{t('settings.general.scan.cooldownSweep')}</Label>
            <Input
              id="scan-sweep" placeholder="15m" className="font-mono"
              aria-invalid={Boolean(errors.scan_cooldown_sweep) || undefined}
              {...register('scan_cooldown_sweep')}
            />
            {errors.scan_cooldown_sweep && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.scan_cooldown_sweep.message ?? '')}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="scan-grace">{t('settings.general.scan.shutdownGrace')}</Label>
            <Input
              id="scan-grace" placeholder="60s" className="font-mono"
              aria-invalid={Boolean(errors.scan_shutdown_grace) || undefined}
              {...register('scan_shutdown_grace')}
            />
            {errors.scan_shutdown_grace && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.scan_shutdown_grace.message ?? '')}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rpm">{t('settings.general.defaults.rpm')}</Label>
            <Input
              id="rpm" type="number" min={0} step={1} className="font-mono"
              aria-invalid={Boolean(errors.global_rpm) || undefined}
              {...register('global_rpm', { valueAsNumber: true })}
            />
            {errors.global_rpm && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.global_rpm.message ?? '')}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="burst">{t('settings.general.defaults.burst')}</Label>
            <Input
              id="burst" type="number" min={0} step={1} className="font-mono"
              aria-invalid={Boolean(errors.global_burst) || undefined}
              {...register('global_burst', { valueAsNumber: true })}
            />
            {errors.global_burst && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.global_burst.message ?? '')}
              </p>
            )}
          </div>
        </div>
        <p className="text-[11.5px] text-muted">{t('settings.general.defaults.rpmHint')}</p>
        <FieldRow
          htmlFor="dry-run"
          label={t('settings.general.defaults.dryRun')}
          hint={t('settings.general.defaults.dryRunHint')}
          control={
            <Switch
              id="dry-run" checked={watch('dry_run')}
              onCheckedChange={(v) => setValue('dry_run', v, { shouldDirty: true })}
            />
          }
        />
      </Block>

      <div className="flex items-center gap-3 pt-4 border-t border-border-faint">
        {isDirty && (
          <span
            data-testid="general-save-bar-dirty"
            className="flex items-center gap-1.5 text-[12.5px] text-status-warn"
          >
            <span className="w-[7px] h-[7px] rounded-full bg-status-warn" aria-hidden="true" />
            {t('settings.saveBar.dirty')}
          </span>
        )}
        <div className="flex-1" />
        <Button
          type="button" variant="ghost"
          disabled={!isDirty || isSubmitting || mut.isPending}
          onClick={onDiscard}
        >
          {t('common.discard')}
        </Button>
        <Button
          type="submit"
          disabled={!isDirty || isSubmitting || mut.isPending || !cronPreview.ok}
        >
          {isSubmitting || mut.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>
    </form>
  );
}
