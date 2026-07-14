import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm, useWatch } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { AlertTriangle, Info, Loader2, Lock, ShieldAlert } from 'lucide-react';
import {
  useRuntimeConfig, useUpdateRuntimeConfig, type RuntimeConfig,
} from '@/lib/runtime-config';
import { useAuthConfig } from '@/lib/auth-config';
import { api } from '@/lib/api';
import { TrustedProxiesEditor } from './TrustedProxiesEditor';
import { isValidCIDR } from '@/lib/cidr';
import type { OIDCFormShape, OIDCTestResult } from './OIDCConfigBlock';
import { OIDCFold } from './OIDCFold';
import { AuthModeSegmented, type AuthMode, AUTH_MODES } from './AuthModeSegmented';
import { AuthModeConfirmDialog } from './AuthModeConfirmDialog';

async function postOIDCTest(payload: {
  issuer?: string; client_id?: string; scopes?: string[];
}): Promise<OIDCTestResult> {
  return api<OIDCTestResult>('/auth/oidc/test', { method: 'POST', body: payload });
}

// ── Zod schema (UNCHANGED from legacy) ───────────────────────────
const schema = z.object({
  session_ttl_min: z.number({ error: 'settings.security.sessions.ttlNumber' })
    .int('settings.security.sessions.ttlInt')
    .min(5, 'settings.security.sessions.ttlMin')
    .max(10080, 'settings.security.sessions.ttlMax'),
  secure_cookie: z.boolean(),
  trusted_proxies: z.array(z.string())
    .refine((arr) => arr.every(isValidCIDR), 'settings.security.proxies.invalid'),
  auth_mode: z.enum(AUTH_MODES),
  oidc_issuer: z.string(),
  oidc_client_id: z.string(),
  oidc_redirect_url: z.string(),
  oidc_scopes: z.array(z.string()),
  oidc_username_claim: z.string(),
  oidc_allowed_groups: z.array(z.string()),
  oidc_groups_claim: z.string(),
}).superRefine((v, ctx) => {
  if (v.auth_mode === 'oidc') {
    if (v.oidc_issuer.trim() === '') {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['oidc_issuer'],
        message: 'settings.security.oidc.issuer.required' });
    }
    if (v.oidc_client_id.trim() === '') {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['oidc_client_id'],
        message: 'settings.security.oidc.clientId.required' });
    }
    if (!v.oidc_scopes.includes('openid')) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['oidc_scopes'],
        message: 'settings.security.oidc.scopes.openidRequired' });
    }
    return;
  }
  const anyPresent = v.oidc_issuer.trim() !== ''
    || v.oidc_client_id.trim() !== '' || v.oidc_redirect_url.trim() !== '';
  const allPresent = v.oidc_issuer.trim() !== '' && v.oidc_client_id.trim() !== '';
  if (anyPresent && !allPresent) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, path: ['oidc_issuer'],
      message: 'settings.security.oidc.partialConfig' });
  }
});
type FormValues = z.infer<typeof schema>;

const HOURS = 60;
const DEFAULT_TTL_MIN = 12 * HOURS;

// parseTTL — UNCHANGED. (Copy from legacy SecurityTab verbatim.)
function parseTTL(raw: string | undefined): number | null {
  if (raw === undefined || raw === null || raw === '') return null;
  const trimmed = raw.trim();
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/g;
  let totalMs = 0, matched = false, cursor = 0;
  for (const m of trimmed.matchAll(re)) {
    matched = true;
    if (m.index !== cursor) return null;
    cursor = m.index + m[0].length;
    const n = Number(m[1]);
    if (!Number.isFinite(n)) return null;
    switch (m[2]) {
      case 'ns': totalMs += n / 1e6; break;
      case 'us': case 'µs': totalMs += n / 1e3; break;
      case 'ms': totalMs += n; break;
      case 's':  totalMs += n * 1000; break;
      case 'm':  totalMs += n * 60_000; break;
      case 'h':  totalMs += n * 3_600_000; break;
      default: return null;
    }
  }
  if (!matched || cursor !== trimmed.length) return null;
  return Math.max(1, Math.round(totalMs / 60_000));
}

function narrowMode(raw: string | undefined): AuthMode {
  return raw === 'basic' || raw === 'none' || raw === 'forms' || raw === 'oidc'
    ? raw : 'forms';
}

function configToForm(c: RuntimeConfig | undefined): FormValues {
  return {
    session_ttl_min: parseTTL(c?.auth?.session_ttl) ?? DEFAULT_TTL_MIN,
    secure_cookie: Boolean(c?.auth?.secure_cookie ?? false),
    trusted_proxies: (c?.auth?.trusted_proxies ?? []) as string[],
    auth_mode: narrowMode(c?.auth?.mode),
    oidc_issuer: c?.auth?.oidc?.issuer ?? '',
    oidc_client_id: c?.auth?.oidc?.client_id ?? '',
    oidc_redirect_url: c?.auth?.oidc?.redirect_url ?? '',
    oidc_scopes: (c?.auth?.oidc?.scopes ?? ['openid', 'profile', 'email']) as string[],
    oidc_username_claim: c?.auth?.oidc?.username_claim ?? 'preferred_username',
    oidc_allowed_groups: (c?.auth?.oidc?.allowed_groups ?? []) as string[],
    oidc_groups_claim: c?.auth?.oidc?.groups_claim ?? 'groups',
  };
}

function formToPayload(
  prev: Partial<RuntimeConfig> | undefined,
  v: FormValues,
  oidcClientSecret: string | undefined,
): RuntimeConfig {
  const base = prev ?? {};
  const { session_epoch: _epoch, ...baseAuthWithoutEpoch } = base.auth ?? {};
  return {
    ...base,
    auth: {
      ...baseAuthWithoutEpoch,
      session_ttl: `${v.session_ttl_min}m`,
      secure_cookie: v.secure_cookie,
      trusted_proxies: v.trusted_proxies,
      mode: v.auth_mode,
      oidc: {
        issuer: v.oidc_issuer.trim(),
        client_id: v.oidc_client_id.trim(),
        redirect_url: v.oidc_redirect_url.trim(),
        scopes: v.oidc_scopes,
        username_claim: v.oidc_username_claim.trim() || 'preferred_username',
        allowed_groups: v.oidc_allowed_groups,
        groups_claim: v.oidc_groups_claim.trim() || 'groups',
        ...(oidcClientSecret !== undefined && { client_secret: oidcClientSecret }),
      },
    },
  } as RuntimeConfig;
}

// ── local .set-block / .field-row helpers ───────────────────────
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

function FieldRow({ label, hint, control }: { label: string; hint?: string; control: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3.5 py-[11px] border-b border-border-faint last:border-b-0">
      <div className="flex flex-col">
        <b className="text-[13.5px] font-[550]">{label}</b>
        {hint && <span className="text-[12px] text-muted">{hint}</span>}
      </div>
      {control}
    </div>
  );
}

export function SecurityTab() {
  const { t } = useTranslation();
  const q = useRuntimeConfig();
  const mut = useUpdateRuntimeConfig();
  const cfg = useAuthConfig();
  const onPlainHTTP =
    typeof window !== 'undefined' && window.location.protocol === 'http:';

  const initialDefaults = useMemo(() => configToForm(undefined), []);
  const {
    register, handleSubmit, reset, control, setValue,
    formState: { errors, isDirty, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: initialDefaults,
    mode: 'onBlur',
  });

  const [oidcClientSecret, setOidcClientSecret] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (q.data?.config && !isDirty) {
      reset(configToForm(q.data.config));
      // reason: oidcClientSecret is an out-of-form side-state used by the
      // OIDCConfigBlock test-connection flow. It must clear whenever the
      // server reseeds the form (otherwise a freshly typed secret would
      // outlive a config refetch). Cannot be expressed as a derived value
      // because it is user-input, not server-derived.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setOidcClientSecret(undefined);
    }
  }, [q.data?.config, isDirty, reset]);

  const storedTTL = q.data?.config?.auth?.session_ttl;
  const ttlParseWarning = useMemo(() => {
    if (!storedTTL) return null;
    return parseTTL(storedTTL) === null ? storedTTL : null;
  }, [storedTTL]);

  const authMode = useWatch({ control, name: 'auth_mode', defaultValue: 'forms' });
  const secureCookie = useWatch({ control, name: 'secure_cookie', defaultValue: false });
  const trustedProxies = useWatch({ control, name: 'trusted_proxies', defaultValue: [] });
  const oidcIssuer = useWatch({ control, name: 'oidc_issuer', defaultValue: '' });
  const oidcClientId = useWatch({ control, name: 'oidc_client_id', defaultValue: '' });
  const oidcRedirectUrl = useWatch({ control, name: 'oidc_redirect_url', defaultValue: '' });
  const oidcScopes = useWatch({ control, name: 'oidc_scopes', defaultValue: ['openid','profile','email'] });
  const oidcUsernameClaim = useWatch({ control, name: 'oidc_username_claim', defaultValue: 'preferred_username' });
  const oidcAllowedGroups = useWatch({ control, name: 'oidc_allowed_groups', defaultValue: [] });
  const oidcGroupsClaim = useWatch({ control, name: 'oidc_groups_claim', defaultValue: 'groups' });

  const oidcReady = Boolean(cfg.data?.oidcReady);
  const showParallelBanner = cfg.data?.mode !== 'oidc' && oidcReady;

  const onSubmit = handleSubmit((values) => {
    mut.mutate(formToPayload(q.data?.config, values, oidcClientSecret), {
      onSuccess: (data) => {
        reset(configToForm(data.config));
        setOidcClientSecret(undefined);
      },
    });
  });
  const onDiscard = () => {
    reset(configToForm(q.data?.config));
    setOidcClientSecret(undefined);
  };

  // Mode-change confirmation flow.
  const [pendingMode, setPendingMode] = useState<AuthMode | null>(null);
  const onModeAttempt = (target: AuthMode) => setPendingMode(target);
  const onModeConfirm = () => {
    if (pendingMode) setValue('auth_mode', pendingMode, { shouldDirty: true });
    setPendingMode(null);
  };

  // OIDC-error-driven force-open.
  const hasOIDCError = Boolean(
    errors.oidc_issuer || errors.oidc_client_id ||
    errors.oidc_redirect_url || errors.oidc_scopes,
  );

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
      <AuthModeSegmented current={authMode} onAttempt={onModeAttempt} />

      <AuthModeConfirmDialog
        open={pendingMode !== null}
        onOpenChange={(o) => { if (!o) setPendingMode(null); }}
        currentMode={authMode}
        targetMode={pendingMode}
        onConfirm={onModeConfirm}
      />

      {authMode === 'none' && (
        <Alert variant="destructive">
          <ShieldAlert className="w-4 h-4" />
          <AlertTitle>{t('settings.security.auth.noneWarningTitle')}</AlertTitle>
          <AlertDescription>{t('settings.security.auth.noneWarning')}</AlertDescription>
        </Alert>
      )}

      {showParallelBanner && (
        <Alert>
          <Info className="w-4 h-4" />
          <AlertTitle>{t('settings.security.oidc.parallelBannerTitle')}</AlertTitle>
          <AlertDescription>
            {t('settings.security.oidc.parallelBannerBody')}{' '}
            <a
              href="/api/v1/auth/oidc/start?next=/settings#security"
              target="_blank" rel="noopener noreferrer" className="underline"
            >
              {t('settings.security.oidc.parallelBannerLink')}
            </a>
          </AlertDescription>
        </Alert>
      )}

      <Block title={t('settings.security.sessions.section')}>
        {ttlParseWarning !== null && (
          <Alert variant="destructive">
            <AlertTriangle className="w-4 h-4" />
            <AlertTitle>{t('settings.security.sessions.ttlUnparseable')}</AlertTitle>
            <AlertDescription>
              {t('settings.security.sessions.ttlUnparseableBody', { value: ttlParseWarning })}
            </AlertDescription>
          </Alert>
        )}

        <div className="grid grid-cols-2 gap-3.5">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ttl">{t('settings.security.sessions.ttl')}</Label>
            <Input
              id="ttl" type="number" min={5} max={10080} step={1} className="font-mono"
              aria-invalid={Boolean(errors.session_ttl_min) || undefined}
              {...register('session_ttl_min', { valueAsNumber: true })}
            />
            <span className="text-[11.5px] text-muted">
              {t('settings.security.sessions.ttlHint')}
            </span>
            {errors.session_ttl_min && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.session_ttl_min.message ?? '')}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="trusted-proxies-row">{t('settings.security.proxies.section')}</Label>
            <TrustedProxiesEditor
              id="trusted-proxies-row"
              value={trustedProxies}
              onChange={(next) => setValue('trusted_proxies', [...next], { shouldDirty: true })}
            />
            {errors.trusted_proxies && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.trusted_proxies.message ?? '')}
              </p>
            )}
          </div>
        </div>

      </Block>

      <Block title={t('settings.security.cookies.section')}>
        {onPlainHTTP ? (
          <Alert>
            <Info className="w-4 h-4" />
            <AlertTitle>{t('settings.security.cookies.tlsNotDetectedTitle')}</AlertTitle>
            <AlertDescription>{t('settings.security.cookies.tlsNotDetectedBody')}</AlertDescription>
          </Alert>
        ) : (
          <FieldRow
            label={t('settings.security.cookies.secure')}
            hint={t('settings.security.cookies.secureHint')}
            control={
              <span className="flex items-center gap-2">
                <Lock className="w-3.5 h-3.5 text-muted" aria-hidden="true" />
                <Switch
                  id="secure"
                  checked={secureCookie}
                  onCheckedChange={(v) => setValue('secure_cookie', v, { shouldDirty: true })}
                />
              </span>
            }
          />
        )}
      </Block>

      <OIDCFold
        mode={authMode}
        forceOpen={hasOIDCError}
        value={{
          issuer: oidcIssuer,
          client_id: oidcClientId,
          redirect_url: oidcRedirectUrl,
          scopes: oidcScopes,
          username_claim: oidcUsernameClaim,
          allowed_groups: oidcAllowedGroups,
          groups_claim: oidcGroupsClaim,
          ...(oidcClientSecret !== undefined && { client_secret: oidcClientSecret }),
          client_secret_configured: Boolean(q.data?.config?.auth?.oidc?.client_secret_configured),
          client_secret_env_override: Boolean(q.data?.config?.auth?.oidc?.client_secret_env_override),
        }}
        onChange={(next: OIDCFormShape) => {
          setValue('oidc_issuer', next.issuer, { shouldDirty: true });
          setValue('oidc_client_id', next.client_id, { shouldDirty: true });
          setValue('oidc_redirect_url', next.redirect_url, { shouldDirty: true });
          setValue('oidc_scopes', [...next.scopes], { shouldDirty: true });
          setValue('oidc_username_claim', next.username_claim, { shouldDirty: true });
          setValue('oidc_allowed_groups', [...next.allowed_groups], { shouldDirty: true });
          setValue('oidc_groups_claim', next.groups_claim, { shouldDirty: true });
          setOidcClientSecret(next.client_secret);
        }}
        onTest={() => {
          const payload: { issuer?: string; client_id?: string; scopes?: string[] } = {};
          const trimIssuer = oidcIssuer.trim();
          const trimClientId = oidcClientId.trim();
          if (trimIssuer) payload.issuer = trimIssuer;
          if (trimClientId) payload.client_id = trimClientId;
          if (oidcScopes.length > 0) payload.scopes = oidcScopes;
          return postOIDCTest(payload);
        }}
        errors={{
          ...(errors.oidc_issuer?.message !== undefined && { issuer: errors.oidc_issuer.message }),
          ...(errors.oidc_client_id?.message !== undefined && { client_id: errors.oidc_client_id.message }),
          ...(errors.oidc_redirect_url?.message !== undefined && { redirect_url: errors.oidc_redirect_url.message }),
          ...(typeof errors.oidc_scopes?.message === 'string' && { scopes: errors.oidc_scopes.message }),
        }}
      />

      <div className="flex items-center gap-3 pt-4 border-t border-border-faint">
        {isDirty && (
          <span
            data-testid="security-save-bar-dirty"
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
        <Button type="submit" disabled={!isDirty || isSubmitting || mut.isPending}>
          {isSubmitting || mut.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>
    </form>
  );
}
