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
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import { AlertTriangle, Info, Loader2, Lock, ShieldAlert } from 'lucide-react';
import {
  useRuntimeConfig, useUpdateRuntimeConfig, type RuntimeConfig,
} from '@/lib/runtime-config';
import { useAuthConfig } from '@/lib/auth-config';
import { api } from '@/lib/api';
import { TrustedProxiesEditor } from './TrustedProxiesEditor';
import { LocalNetworksEditor, LOCAL_NETWORK_DEFAULTS } from './LocalNetworksEditor';
import { isValidCIDR } from '@/lib/cidr';
import { OIDCConfigBlock, type OIDCFormShape, type OIDCTestResult } from './OIDCConfigBlock';

async function postOIDCTest(payload: {
  issuer?: string; client_id?: string; scopes?: string[];
}): Promise<OIDCTestResult> {
  return api<OIDCTestResult>('/auth/oidc/test', {
    method: 'POST',
    body: payload,
  });
}

const AUTH_MODES = ['forms', 'basic', 'none', 'oidc'] as const;
type AuthModeValue = (typeof AUTH_MODES)[number];

const schema = z.object({
  session_ttl_min: z
    .number({ error: 'settings.security.sessions.ttlNumber' })
    .int('settings.security.sessions.ttlInt')
    .min(5, 'settings.security.sessions.ttlMin')
    .max(10080, 'settings.security.sessions.ttlMax'),
  secure_cookie: z.boolean(),
  trusted_proxies: z
    .array(z.string())
    .refine((arr) => arr.every(isValidCIDR), 'settings.security.proxies.invalid'),
  auth_mode: z.enum(AUTH_MODES),
  auth_local_bypass: z.boolean(),
  auth_local_networks: z
    .array(z.string())
    .refine((arr) => arr.every(isValidCIDR), 'settings.security.localNetworks.invalid'),
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
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['oidc_issuer'],
        message: 'settings.security.oidc.issuer.required',
      });
    }
    if (v.oidc_client_id.trim() === '') {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['oidc_client_id'],
        message: 'settings.security.oidc.clientId.required',
      });
    }
    if (!v.oidc_scopes.includes('openid')) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['oidc_scopes'],
        message: 'settings.security.oidc.scopes.openidRequired',
      });
    }
    return;
  }
  // mode != oidc: partial OIDC is an error; full OIDC is fine.
  const anyPresent =
    v.oidc_issuer.trim() !== '' ||
    v.oidc_client_id.trim() !== '' ||
    v.oidc_redirect_url.trim() !== '';
  const allPresent = v.oidc_issuer.trim() !== '' && v.oidc_client_id.trim() !== '';
  if (anyPresent && !allPresent) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['oidc_issuer'],
      message: 'settings.security.oidc.partialConfig',
    });
  }
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

function narrowMode(raw: string | undefined): AuthModeValue {
  return raw === 'basic' || raw === 'none' || raw === 'forms' || raw === 'oidc'
    ? raw
    : 'forms';
}

function configToForm(c: RuntimeConfig | undefined): FormValues {
  return {
    session_ttl_min: parseTTL(c?.auth?.session_ttl) ?? DEFAULT_TTL_MIN,
    secure_cookie: Boolean(c?.auth?.secure_cookie ?? false),
    trusted_proxies: (c?.auth?.trusted_proxies ?? []) as string[],
    auth_mode: narrowMode(c?.auth?.mode),
    auth_local_bypass: Boolean(c?.auth?.local_bypass ?? false),
    auth_local_networks: (c?.auth?.local_networks ?? []) as string[],
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
      local_bypass: v.auth_local_bypass,
      local_networks: v.auth_local_networks,
      oidc: {
        issuer: v.oidc_issuer.trim(),
        client_id: v.oidc_client_id.trim(),
        redirect_url: v.oidc_redirect_url.trim(),
        scopes: v.oidc_scopes,
        username_claim: v.oidc_username_claim.trim() || 'preferred_username',
        allowed_groups: v.oidc_allowed_groups,
        groups_claim: v.oidc_groups_claim.trim() || 'groups',
        // client_secret: only included when dirty-bit is set (undefined = preserve existing).
        ...(oidcClientSecret !== undefined && { client_secret: oidcClientSecret }),
      },
      // session_epoch deliberately omitted — server manages it.
    },
  } as RuntimeConfig;
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

  // oidcClientSecret follows the dirty-bit pattern:
  //   undefined → preserve existing (not sent in PUT)
  //   string    → send as-is (empty string clears the stored secret)
  // Seeded from the server on load/save; set to undefined on discard.
  const [oidcClientSecret, setOidcClientSecret] = useState<string | undefined>(undefined);

  // Sync form to fresh server data only while the form is pristine, so a
  // background refetch can't clobber unsaved edits. We hold off until
  // isDirty clears (via Discard or a successful save).
  useEffect(() => {
    if (q.data?.config && !isDirty) {
      reset(configToForm(q.data.config));
      // Reset the client secret dirty-bit on pristine sync (discard / post-save).
      setOidcClientSecret(undefined);
    }
  }, [q.data?.config, isDirty, reset]);

  const storedTTL = q.data?.config?.auth?.session_ttl;
  const ttlParseWarning = useMemo(() => {
    if (storedTTL === undefined || storedTTL === null || storedTTL === '') return null;
    return parseTTL(storedTTL) === null ? storedTTL : null;
  }, [storedTTL]);

  const onSubmit = handleSubmit((values) => {
    mut.mutate(formToPayload(q.data?.config, values, oidcClientSecret), {
      // Reset to persisted values so isDirty clears after a save.
      onSuccess: (data) => {
        reset(configToForm(data.config));
        setOidcClientSecret(undefined);
      },
    });
  });

  const authMode = useWatch({ control, name: 'auth_mode', defaultValue: 'forms' });
  const localBypass = useWatch({ control, name: 'auth_local_bypass', defaultValue: false });
  const localNetworks = useWatch({ control, name: 'auth_local_networks', defaultValue: [] });
  const secureCookie = useWatch({ control, name: 'secure_cookie', defaultValue: false });
  const trustedProxies = useWatch({ control, name: 'trusted_proxies', defaultValue: [] });
  const oidcIssuer = useWatch({ control, name: 'oidc_issuer', defaultValue: '' });
  const oidcClientId = useWatch({ control, name: 'oidc_client_id', defaultValue: '' });
  const oidcRedirectUrl = useWatch({ control, name: 'oidc_redirect_url', defaultValue: '' });
  const oidcScopes = useWatch({ control, name: 'oidc_scopes', defaultValue: ['openid', 'profile', 'email'] });
  const oidcUsernameClaim = useWatch({ control, name: 'oidc_username_claim', defaultValue: 'preferred_username' });
  const oidcAllowedGroups = useWatch({ control, name: 'oidc_allowed_groups', defaultValue: [] });
  const oidcGroupsClaim = useWatch({ control, name: 'oidc_groups_claim', defaultValue: 'groups' });

  const oidcReady = Boolean(cfg.data?.oidcReady);
  const showParallelBanner = cfg.data?.mode !== 'oidc' && oidcReady;

  const onDiscard = () => {
    reset(configToForm(q.data?.config));
    setOidcClientSecret(undefined);
  };

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

  // Migrate empty-on-load: when the user toggles bypass ON for the first
  // time we want a sensible starting set rather than an empty CIDR list,
  // which would semantically mean "no addresses are local" and silently
  // defeat the toggle. Seed defaults ONLY if the list is currently empty.
  const onBypassChange = (next: 'enabled' | 'disabledLocal') => {
    const enabled = next === 'disabledLocal';
    setValue('auth_local_bypass', enabled, { shouldDirty: true });
    if (enabled && localNetworks.length === 0) {
      setValue('auth_local_networks', [...LOCAL_NETWORK_DEFAULTS], { shouldDirty: true });
    }
  };

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-6" noValidate>
      <section className="flex flex-col gap-4">
        <h3 className="text-[14px] font-semibold tracking-tight">{t('settings.security.auth.section')}</h3>

        <div className="flex flex-col gap-1.5 max-w-md">
          <Label htmlFor="auth-mode">{t('settings.security.auth.modeLabel')}</Label>
          <Select
            value={authMode}
            onValueChange={(v) => {
              if (v) setValue('auth_mode', v as AuthModeValue, { shouldDirty: true });
            }}
          >
            <SelectTrigger id="auth-mode">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="forms">{t('settings.security.auth.modes.forms')}</SelectItem>
              <SelectItem value="basic">{t('settings.security.auth.modes.basic')}</SelectItem>
              <SelectItem value="none">{t('settings.security.auth.modes.none')}</SelectItem>
              <SelectItem value="oidc">{t('settings.security.auth.modes.oidc')}</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-[11.5px] text-muted">
            {t(`settings.security.auth.modeHints.${authMode}`)}
          </p>
        </div>

        {authMode === 'none' && (
          <Alert variant="destructive">
            <ShieldAlert className="w-4 h-4" />
            <AlertTitle>{t('settings.security.auth.noneWarningTitle')}</AlertTitle>
            <AlertDescription>{t('settings.security.auth.noneWarning')}</AlertDescription>
          </Alert>
        )}

        <>
          {showParallelBanner && (
            <Alert>
              <Info className="w-4 h-4" />
              <AlertTitle>{t('settings.security.oidc.parallelBannerTitle')}</AlertTitle>
              <AlertDescription>
                {t('settings.security.oidc.parallelBannerBody')}{' '}
                <a href="/api/v1/auth/oidc/start?next=/settings#security"
                   target="_blank" rel="noopener noreferrer" className="underline">
                  {t('settings.security.oidc.parallelBannerLink')}
                </a>
              </AlertDescription>
            </Alert>
          )}
          <OIDCConfigBlock
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
              const payload: Parameters<typeof postOIDCTest>[0] = {};
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
        </>

        <div className="flex flex-col gap-1.5 max-w-md">
          <Label htmlFor="auth-required">{t('settings.security.auth.requiredLabel')}</Label>
          <Select
            value={localBypass ? 'disabledLocal' : 'enabled'}
            onValueChange={(v) => {
              if (v) onBypassChange(v as 'enabled' | 'disabledLocal');
            }}
          >
            <SelectTrigger id="auth-required">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="enabled">{t('settings.security.auth.required.enabled')}</SelectItem>
              <SelectItem value="disabledLocal">{t('settings.security.auth.required.disabledLocal')}</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-[11.5px] text-muted">
            {t('settings.security.auth.required.hint')}
          </p>
        </div>

        {localBypass && (
          <div className="flex flex-col gap-2">
            <Label htmlFor="local-networks">{t('settings.security.localNetworks.section')}</Label>
            <p className="text-[12px] text-muted">
              {t('settings.security.localNetworks.hint')}
            </p>
            <LocalNetworksEditor
              id="local-networks"
              value={localNetworks}
              onChange={(next) =>
                setValue('auth_local_networks', [...next], { shouldDirty: true })
              }
            />
            {errors.auth_local_networks && (
              <p role="alert" className="text-[11.5px] text-status-danger">
                {t(errors.auth_local_networks.message ?? '')}
              </p>
            )}
          </div>
        )}
      </section>

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
              checked={secureCookie}
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
          value={trustedProxies}
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
