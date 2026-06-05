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
import logoUrl from '@/assets/logo.svg';

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

  // Mode-aware redirect: under mode=basic the browser popup handles auth,
  // so landing on /login is a stale URL and we send the user home.
  useEffect(() => {
    if (cfg.isSuccess && cfg.data.mode === 'basic') {
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
          <img src={logoUrl} alt="" className="mx-auto w-16 h-16" />
          <Skeleton className="h-6 w-32" />
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      </div>
    );
  }

  const next = safeNext(params.get('next'));
  const oidcReady = cfg.data?.oidcReady ?? false;

  // mode=oidc → SSO-only screen.
  if (cfg.isSuccess && cfg.data.mode === 'oidc') {
    const href = ssoHref(cfg.data.loginUrl, next);
    return (
      <div className="min-h-screen grid place-items-center bg-bg px-4">
        <div className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5">
          <div>
            <img src={logoUrl} alt="" className="w-12 h-12 mb-3" />
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

  // mode=none → entry button (+ optional SSO button).
  if (cfg.isSuccess && cfg.data.mode === 'none') {
    return (
      <div className="min-h-screen grid place-items-center bg-bg px-4">
        <div className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5">
          <div>
            <img src={logoUrl} alt="" className="w-12 h-12 mb-3" />
            <h1 className="text-[22px] font-semibold tracking-tight">{t('app.name').toLowerCase()}</h1>
            <p className="text-muted text-[13px] mt-1">{t('app.tagline')}</p>
          </div>
          <Button asChild className="h-10 font-semibold">
            <a href={safeNext(params.get('next'))}>
              {t('login.enter')}
            </a>
          </Button>
          {oidcReady && (
            <>
              <p className="text-center text-[12px] text-muted">{t('login.or')}</p>
              <Button asChild variant="outline" className="h-10 font-semibold">
                <a href={ssoHref(cfg.data.loginUrl, next)} data-testid="oidc-login-link">
                  {t('login.sso.button')}
                </a>
              </Button>
            </>
          )}
        </div>
      </div>
    );
  }

  // Default: mode=forms → password form (+ optional SSO button below).
  return (
    <div className="min-h-screen grid place-items-center bg-bg px-4">
      <form
        onSubmit={onSubmit}
        autoComplete="on"
        className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5"
        noValidate
      >
        <div>
          <img src={logoUrl} alt="" className="w-12 h-12 mb-3" />
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

        {oidcReady && (
          <>
            <p className="text-center text-[12px] text-muted">{t('login.or')}</p>
            <Button asChild variant="outline" className="h-10 font-semibold">
              <a href={ssoHref(cfg.data?.loginUrl, next)} data-testid="oidc-login-link">
                {t('login.sso.button')}
              </a>
            </Button>
          </>
        )}
      </form>
    </div>
  );
}

function ssoHref(loginUrl: string | undefined, next: string): string {
  const base = loginUrl ?? '/api/v1/auth/oidc/start';
  return next === '/' ? base : `${base}?next=${encodeURIComponent(next)}`;
}
