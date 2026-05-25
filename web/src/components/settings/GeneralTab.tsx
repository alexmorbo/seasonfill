import { useEffect, useMemo, useState } from 'react';
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
import {
  useRuntimeConfig, useUpdateRuntimeConfig, type RuntimeConfig,
} from '@/lib/runtime-config';

// Go duration regex: 30s, 12h, 500ms, 1h30m, etc. Mirrors
// time.ParseDuration's acceptable shape (units: ns, us, µs, ms,
// s, m, h). We don't bother with the negative form — durations on
// runtime config are always non-negative.
const goDurRE = /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/;

const schema = z.object({
  cron_enabled: z.boolean(),
  cron_schedule: z.string().min(1, 'Schedule required'),
  cron_on_start: z.boolean(),
  cron_jitter: z.string().regex(goDurRE, 'Use a Go duration like "1m" or "30s"'),
  scan_shutdown_grace: z.string().regex(goDurRE, 'Use a Go duration'),
  scan_cooldown_sweep: z.string().regex(goDurRE, 'Use a Go duration'),
  dry_run: z.boolean(),
  global_rpm: z.number().int().min(0, 'Must be ≥ 0'),
  global_burst: z.number().int().min(0, 'Must be ≥ 0'),
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

function formToPayload(prev: RuntimeConfig | undefined, v: FormValues): RuntimeConfig {
  // Merge over the last-known full config so we preserve fields that
  // belong to the Security tab and any future ones we don't yet model.
  const base = prev ?? ({} as RuntimeConfig);
  return {
    ...base,
    cron: {
      enabled: v.cron_enabled,
      schedule: v.cron_schedule,
      on_start: v.cron_on_start,
      jitter: v.cron_jitter,
    },
    scan: {
      shutdown_grace: v.scan_shutdown_grace,
      cooldown_sweep: v.scan_cooldown_sweep,
    },
    dry_run: v.dry_run,
    global_rate_limit: { rpm: v.global_rpm, burst: v.global_burst },
  };
}

function describeCron(expr: string): { ok: boolean; text: string } {
  try {
    const text = cronstrue.toString(expr, { throwExceptionOnParseError: true });
    return { ok: true, text };
  } catch {
    return { ok: false, text: 'Invalid cron expression' };
  }
}

export function GeneralTab() {
  const q = useRuntimeConfig();
  const mut = useUpdateRuntimeConfig();
  const [initialKey, setInitialKey] = useState(0);

  const {
    register, handleSubmit, reset, watch, setValue,
    formState: { errors, isDirty, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: configToForm(undefined),
    mode: 'onBlur',
  });

  useEffect(() => {
    if (q.data?.config) {
      reset(configToForm(q.data.config));
      setInitialKey((n) => n + 1);
    }
  }, [q.data?.config, reset]);

  const cronVal = watch('cron_schedule');
  const cronPreview = useMemo(() => describeCron(cronVal), [cronVal]);

  const onSubmit = handleSubmit(async (values) => {
    await mut.mutateAsync(formToPayload(q.data?.config, values));
  });

  const onDiscard = () => {
    reset(configToForm(q.data?.config));
  };

  if (q.isPending) {
    return (
      <div className="flex items-center gap-2 text-muted text-[13px]">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> Loading settings…
      </div>
    );
  }
  if (q.isError) {
    return (
      <Alert variant="destructive">
        <AlertTriangle className="w-4 h-4" />
        <AlertTitle>Failed to load runtime config</AlertTitle>
        <AlertDescription>{q.error.message}</AlertDescription>
      </Alert>
    );
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-6" noValidate key={initialKey}>
      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">Schedule</h3>

        <div className="flex items-center justify-between gap-3">
          <div>
            <Label htmlFor="cron-enabled">Scheduled scans enabled</Label>
            <p className="text-[11.5px] text-muted">
              Webhook scanning continues even when this is off.
            </p>
          </div>
          <Switch
            id="cron-enabled"
            checked={watch('cron_enabled')}
            onCheckedChange={(v) => setValue('cron_enabled', v, { shouldDirty: true })}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cron-schedule">Cron expression</Label>
          <Input
            id="cron-schedule" placeholder="0 */6 * * *"
            aria-invalid={Boolean(errors.cron_schedule) || undefined}
            {...register('cron_schedule')}
          />
          <p
            className={
              cronPreview.ok
                ? 'text-[11.5px] text-muted'
                : 'text-[11.5px] text-status-danger'
            }
            role={cronPreview.ok ? undefined : 'alert'}
          >
            {cronPreview.text}
          </p>
          {errors.cron_schedule && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {errors.cron_schedule.message}
            </p>
          )}
        </div>

        <div className="flex items-center justify-between gap-3">
          <Label htmlFor="cron-on-start">Run once on server start</Label>
          <Switch
            id="cron-on-start"
            checked={watch('cron_on_start')}
            onCheckedChange={(v) => setValue('cron_on_start', v, { shouldDirty: true })}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cron-jitter">Jitter</Label>
          <Input
            id="cron-jitter" placeholder="1m"
            aria-invalid={Boolean(errors.cron_jitter) || undefined}
            {...register('cron_jitter')}
          />
          {errors.cron_jitter && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {errors.cron_jitter.message}
            </p>
          )}
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">Scan tuning</h3>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="scan-grace">Shutdown grace</Label>
            <Input
              id="scan-grace" placeholder="60s"
              aria-invalid={Boolean(errors.scan_shutdown_grace) || undefined}
              {...register('scan_shutdown_grace')}
            />
            {errors.scan_shutdown_grace && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.scan_shutdown_grace.message}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="scan-sweep">Cooldown sweep</Label>
            <Input
              id="scan-sweep" placeholder="15m"
              aria-invalid={Boolean(errors.scan_cooldown_sweep) || undefined}
              {...register('scan_cooldown_sweep')}
            />
            {errors.scan_cooldown_sweep && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.scan_cooldown_sweep.message}
              </p>
            )}
          </div>
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">
          Defaults & limits
        </h3>

        <div className="flex items-center justify-between gap-3">
          <div>
            <Label htmlFor="dry-run">Dry run by default</Label>
            <p className="text-[11.5px] text-muted">
              Scans evaluate decisions but never call Sonarr grab endpoints.
            </p>
          </div>
          <Switch
            id="dry-run"
            checked={watch('dry_run')}
            onCheckedChange={(v) => setValue('dry_run', v, { shouldDirty: true })}
          />
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rpm">Global rate limit (rpm)</Label>
            <Input
              id="rpm" type="number" min={0} step={1}
              aria-invalid={Boolean(errors.global_rpm) || undefined}
              {...register('global_rpm', { valueAsNumber: true })}
            />
            {errors.global_rpm && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.global_rpm.message}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="burst">Burst</Label>
            <Input
              id="burst" type="number" min={0} step={1}
              aria-invalid={Boolean(errors.global_burst) || undefined}
              {...register('global_burst', { valueAsNumber: true })}
            />
            {errors.global_burst && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.global_burst.message}
              </p>
            )}
          </div>
        </div>
        <p className="text-[11.5px] text-muted">
          Set rpm = 0 to disable the global limiter.
        </p>
      </section>

      <div className="flex justify-end gap-2 pt-2 border-t border-border">
        <Button type="button" variant="ghost" disabled={!isDirty || isSubmitting} onClick={onDiscard}>
          Discard
        </Button>
        <Button type="submit" disabled={!isDirty || isSubmitting || !cronPreview.ok}>
          {isSubmitting ? 'Saving…' : 'Save'}
        </Button>
      </div>
    </form>
  );
}
