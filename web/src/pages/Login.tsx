import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { ApiError } from '@/lib/api';
import { loginWithPassword, sessionQueryKey } from '@/lib/auth';
import { useAuthConfig } from '@/lib/auth-config';

const schema = z.object({
  username: z.string().min(1, 'login.usernameRequired'),
  password: z.string().min(1, 'login.passwordRequired'),
});
type FormValues = z.infer<typeof schema>;

function safeNext(raw: string | null): string {
  if (!raw) return '/';
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/';
  return raw;
}

export function Login() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const cfg = useAuthConfig();
  const [serverErr, setServerErr] = useState<string | null>(null);
  const { register, handleSubmit, formState: { errors, isSubmitting } } =
    useForm<FormValues>({
      resolver: zodResolver(schema),
      defaultValues: { username: '', password: '' },
    });

  // Mode-aware redirect: under mode=basic / mode=none the browser popup
  // or open-access path handles auth, so landing on /login is a stale
  // URL and we send the user home. mode=oidc renders the SSO button
  // below instead (no auto-redirect — the user explicitly clicks).
  useEffect(() => {
    if (cfg.isSuccess && (cfg.data.mode === 'basic' || cfg.data.mode === 'none')) {
      navigate(safeNext(params.get('next')), { replace: true });
    }
  }, [cfg.isSuccess, cfg.data?.mode, navigate, params]);

  const onSubmit = handleSubmit(async ({ username, password }) => {
    setServerErr(null);
    try {
      await loginWithPassword({ username, password });
      await qc.invalidateQueries({ queryKey: sessionQueryKey });
      navigate(safeNext(params.get('next')), { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401 || err.status === 429) {
          setServerErr(t('login.invalid'));
        } else if (err.status >= 500) {
          setServerErr(t('login.serviceUnavailable'));
        } else {
          setServerErr(err.message || t('login.invalid'));
        }
      } else {
        setServerErr(err instanceof Error ? err.message : t('login.invalid'));
      }
    }
  });

  // While the config is in flight, render a skeleton — avoids flashing
  // the password form on a mode=oidc deployment before the SSO button
  // would render.
  if (cfg.isPending) {
    return (
      <div className="min-h-screen grid place-items-center bg-bg px-4">
        <div className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5">
          <Skeleton className="h-6 w-32" />
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      </div>
    );
  }

  // mode=oidc → render a single "Login with SSO" anchor that drives
  // a full-page navigation to /api/v1/auth/oidc/start. The backend
  // handler sets the PKCE/nonce/state cookies and 302s to the provider.
  if (cfg.isSuccess && cfg.data.mode === 'oidc') {
    const next = safeNext(params.get('next'));
    const loginHref = cfg.data.loginUrl ?? '/api/v1/auth/oidc/start';
    const href = next === '/' ? loginHref : `${loginHref}?next=${encodeURIComponent(next)}`;
    return (
      <div className="min-h-screen grid place-items-center bg-bg px-4">
        <div className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5">
          <div>
            <h1 className="text-[22px] font-semibold tracking-tight">{t('app.name').toLowerCase()}</h1>
            <p className="text-muted text-[13px] mt-1">{t('app.tagline')}</p>
          </div>
          <p className="text-[13px] text-foreground-2">
            {t('login.sso.intro')}
          </p>
          <Button asChild className="h-10 font-semibold">
            <a href={href} data-testid="oidc-login-link">
              {t('login.sso.button')}
            </a>
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen grid place-items-center bg-bg px-4">
      <form
        onSubmit={onSubmit}
        autoComplete="on"
        className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5"
        noValidate
      >
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">{t('app.name').toLowerCase()}</h1>
          <p className="text-muted text-[13px] mt-1">{t('app.tagline')}</p>
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="username" className="text-[12px] text-foreground-2">
            {t('login.username')}
          </Label>
          <Input
            id="username"
            type="text"
            autoFocus
            autoComplete="username"
            aria-invalid={Boolean(errors.username) || undefined}
            {...register('username')}
          />
          {errors.username && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t(errors.username.message ?? 'login.usernameRequired')}
            </p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="password" className="text-[12px] text-foreground-2">
            {t('login.password')}
          </Label>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            aria-invalid={Boolean(errors.password || serverErr) || undefined}
            {...register('password')}
          />
          {errors.password && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {t(errors.password.message ?? 'login.passwordRequired')}
            </p>
          )}
          {serverErr && !errors.password && !errors.username && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {serverErr}
            </p>
          )}
        </div>

        <Button type="submit" disabled={isSubmitting} className="h-10 font-semibold">
          {isSubmitting ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>
    </div>
  );
}
