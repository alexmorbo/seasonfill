import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { AlertTriangle, Info, Loader2, Lock } from 'lucide-react';
import {
  useRuntimeConfig, useUpdateRuntimeConfig, type RuntimeConfig,
} from '@/lib/runtime-config';
import { TrustedProxiesEditor, isValidCIDR } from './TrustedProxiesEditor';

const schema = z.object({
  session_ttl_min: z
    .number({ invalid_type_error: 'settings.security.sessions.ttlNumber' })
    .int('settings.security.sessions.ttlInt')
    .min(5, 'settings.security.sessions.ttlMin')
    .max(10080, 'settings.security.sessions.ttlMax'),
  secure_cookie: z.boolean(),
  trusted_proxies: z
    .array(z.string())
    .refine((arr) => arr.every(isValidCIDR), 'settings.security.proxies.invalid'),
});
type FormValues = z.infer<typeof schema>;

const HOURS = 60;
const DEFAULT_TTL_MIN = 12 * HOURS;

// parseTTL accepts the full Go duration format including compound forms
// like "12h0m0s" (what time.Duration.String() emits for round hours).
// Returns minutes, or null when the input is unparseable so callers can
// surface an explicit warning rather than silently overwriting the row.
function parseTTL(raw: string | undefined): number | null {
  if (raw === undefined || raw === null || raw === '') return null;
  const trimmed = raw.trim();
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/g;
  let totalMs = 0;
  let matched = false;
  let cursor = 0;
  for (const m of trimmed.matchAll(re)) {
    matched = true;
    if (m.index !== cursor) return null;
    cursor = m.index + m[0].length;
    const n = Number(m[1]);
    if (!Number.isFinite(n)) return null;
    switch (m[2]) {
      case 'ns': totalMs += n / 1e6; break;
      case 'us':
      case 'µs': totalMs += n / 1e3; break;
      case 'ms': totalMs += n; break;
      case 's':  totalMs += n * 1000; break;
      case 'm':  totalMs += n * 60_000; break;
      case 'h':  totalMs += n * 3_600_000; break;
      default: return null;
    }
  }
  if (!matched || cursor !== trimmed.length) return null;
  const minutes = totalMs / 60_000;
  return Math.max(1, Math.round(minutes));
}

function configToForm(c: RuntimeConfig | undefined): FormValues {
  return {
    session_ttl_min: parseTTL(c?.auth?.session_ttl) ?? DEFAULT_TTL_MIN,
    secure_cookie: Boolean(c?.auth?.secure_cookie ?? false),
    trusted_proxies: (c?.auth?.trusted_proxies ?? []) as string[],
  };
}

function formToPayload(prev: Partial<RuntimeConfig> | undefined, v: FormValues): RuntimeConfig {
  const base = prev ?? {};
  return {
    ...base,
    auth: {
      ...(base.auth ?? {}),
      session_ttl: `${v.session_ttl_min}m`,
      secure_cookie: v.secure_cookie,
      trusted_proxies: v.trusted_proxies,
    },
  } as RuntimeConfig;
}

export function SecurityTab() {
  const { t } = useTranslation();
  const q = useRuntimeConfig();
  const mut = useUpdateRuntimeConfig();
  const onPlainHTTP =
    typeof window !== 'undefined' && window.location.protocol === 'http:';

  const {
    register, handleSubmit, reset, watch, setValue,
    formState: { errors, isDirty, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: configToForm(undefined),
    mode: 'onBlur',
  });

  // Sync form to fresh server data only while the form is pristine, so a
  // background refetch can't clobber unsaved edits. We hold off until
  // isDirty clears (via Discard or a successful save).
  useEffect(() => {
    if (q.data?.config && !isDirty) {
      reset(configToForm(q.data.config));
    }
  }, [q.data?.config, isDirty, reset]);

  const storedTTL = q.data?.config?.auth?.session_ttl;
  const ttlParseWarning = useMemo(() => {
    if (storedTTL === undefined || storedTTL === null || storedTTL === '') return null;
    return parseTTL(storedTTL) === null ? storedTTL : null;
  }, [storedTTL]);

  const onSubmit = handleSubmit((values) => {
    mut.mutate(formToPayload(q.data?.config, values), {
      // Reset to persisted values so isDirty clears after a save.
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
    <form onSubmit={onSubmit} className="flex flex-col gap-6" noValidate>
      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">{t('settings.security.sessions.section')}</h3>

        {ttlParseWarning !== null && (
          <Alert variant="destructive">
            <AlertTriangle className="w-4 h-4" />
            <AlertTitle>{t('settings.security.sessions.ttlUnparseable')}</AlertTitle>
            <AlertDescription>
              {t('settings.security.sessions.ttlUnparseableBody', { value: ttlParseWarning })}
            </AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-1.5 max-w-xs">
          <Label htmlFor="ttl">{t('settings.security.sessions.ttl')}</Label>
          <Input
            id="ttl"
            type="number"
            min={5}
            max={10080}
            step={1}
            aria-invalid={Boolean(errors.session_ttl_min) || undefined}
            {...register('session_ttl_min', { valueAsNumber: true })}
          />
          <p className="text-[11.5px] text-muted">
            {t('settings.security.sessions.ttlHint')}
          </p>
          {errors.session_ttl_min && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t(errors.session_ttl_min.message ?? '')}
            </p>
          )}
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">{t('settings.security.cookies.section')}</h3>

        {onPlainHTTP ? (
          <Alert>
            <Info className="w-4 h-4" />
            <AlertTitle>{t('settings.security.cookies.tlsNotDetectedTitle')}</AlertTitle>
            <AlertDescription>
              {t('settings.security.cookies.tlsNotDetectedBody')}
            </AlertDescription>
          </Alert>
        ) : (
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <Lock className="w-3.5 h-3.5 text-muted" />
              <div>
                <Label htmlFor="secure">{t('settings.security.cookies.secure')}</Label>
                <p className="text-[11.5px] text-muted">
                  {t('settings.security.cookies.secureHint')}
                </p>
              </div>
            </div>
            <Switch
              id="secure"
              checked={watch('secure_cookie')}
              onCheckedChange={(v) => setValue('secure_cookie', v, { shouldDirty: true })}
            />
          </div>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <h3 className="text-[14px] font-semibold tracking-tight">{t('settings.security.proxies.section')}</h3>
        <p className="text-[12px] text-muted">
          {t('settings.security.proxies.hint')}
        </p>
        <TrustedProxiesEditor
          id="proxies"
          value={watch('trusted_proxies')}
          onChange={(next) => setValue('trusted_proxies', [...next], { shouldDirty: true })}
        />
        {errors.trusted_proxies && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(errors.trusted_proxies.message ?? '')}
          </p>
        )}
      </section>

      <div className="flex justify-end gap-2 pt-2 border-t border-border">
        <Button
          type="button" variant="ghost"
          disabled={!isDirty || isSubmitting || mut.isPending} onClick={onDiscard}
        >
          {t('common.discard')}
        </Button>
        <Button type="submit" disabled={!isDirty || isSubmitting || mut.isPending}>
          {isSubmitting || mut.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>
    </form>
  );
}
