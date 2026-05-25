import { useEffect, useState } from 'react';
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
    .number({ invalid_type_error: 'Must be a number' })
    .int('Must be a whole number')
    .min(5, 'Minimum 5 minutes')
    .max(10080, 'Maximum 10080 minutes (7 days)'),
  secure_cookie: z.boolean(),
  trusted_proxies: z
    .array(z.string())
    .refine((arr) => arr.every(isValidCIDR), 'One or more entries are invalid'),
});
type FormValues = z.infer<typeof schema>;

const HOURS = 60;

function parseTTL(raw: string | undefined): number {
  if (!raw) return 12 * HOURS;
  const m = /^(\d+(?:\.\d+)?)(h|m|s)$/.exec(raw.trim());
  if (!m) return 12 * HOURS;
  const n = Number(m[1]);
  switch (m[2]) {
    case 'h': return Math.round(n * HOURS);
    case 'm': return Math.round(n);
    case 's': return Math.max(1, Math.round(n / 60));
    default: return 12 * HOURS;
  }
}

function configToForm(c: RuntimeConfig | undefined): FormValues {
  return {
    session_ttl_min: parseTTL(c?.auth?.session_ttl),
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

  const [formKey, setFormKey] = useState(0);
  useEffect(() => {
    if (q.data?.config) {
      reset(configToForm(q.data.config));
      setFormKey((n) => n + 1);
    }
  }, [q.data?.config, reset]);

  const onSubmit = handleSubmit((values) => {
    mut.mutate(formToPayload(q.data?.config, values));
  });

  const onDiscard = () => reset(configToForm(q.data?.config));

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
    <form onSubmit={onSubmit} className="flex flex-col gap-6" noValidate key={formKey}>
      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">Sessions</h3>

        <div className="flex flex-col gap-1.5 max-w-xs">
          <Label htmlFor="ttl">Session TTL (minutes)</Label>
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
            Range: 5 minutes to 7 days (10080 minutes).
          </p>
          {errors.session_ttl_min && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {errors.session_ttl_min.message}
            </p>
          )}
        </div>
      </section>

      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">Cookies</h3>

        {onPlainHTTP ? (
          <Alert>
            <Info className="w-4 h-4" />
            <AlertTitle>TLS not detected (running on http://)</AlertTitle>
            <AlertDescription>
              The "Secure cookie" switch is disabled — enable it once an
              HTTPS-terminating proxy or Ingress is in front.
            </AlertDescription>
          </Alert>
        ) : (
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <Lock className="w-3.5 h-3.5 text-muted" />
              <div>
                <Label htmlFor="secure">Secure cookie</Label>
                <p className="text-[11.5px] text-muted">
                  Adds the <span className="font-mono">Secure</span> flag to
                  the session cookie.
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
        <h3 className="text-[14px] font-semibold tracking-tight">Trusted proxies</h3>
        <p className="text-[12px] text-muted">
          Requests from these IPs / CIDR blocks may carry an
          <span className="font-mono"> X-Forwarded-For</span> header that we
          trust. List your Ingress / reverse-proxy ranges only.
        </p>
        <TrustedProxiesEditor
          id="proxies"
          value={watch('trusted_proxies')}
          onChange={(next) => setValue('trusted_proxies', [...next], { shouldDirty: true })}
        />
        {errors.trusted_proxies && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {errors.trusted_proxies.message}
          </p>
        )}
      </section>

      <div className="flex justify-end gap-2 pt-2 border-t border-border">
        <Button
          type="button" variant="ghost"
          disabled={!isDirty || isSubmitting} onClick={onDiscard}
        >
          Discard
        </Button>
        <Button type="submit" disabled={!isDirty || isSubmitting}>
          {isSubmitting ? 'Saving…' : 'Save'}
        </Button>
      </div>
    </form>
  );
}
