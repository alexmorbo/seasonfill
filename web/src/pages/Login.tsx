import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowRight, KeyRound, LogIn, ShieldCheck, User } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { ApiError } from '@/lib/api';
import { loginWithPassword, sessionQueryKey, useSession } from '@/lib/auth';
import { useAuthConfig, type AuthConfig } from '@/lib/auth-config';
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

function ssoHref(loginUrl: string | undefined, next: string): string {
  const base = loginUrl ?? '/api/v1/auth/oidc/start';
  return next === '/' ? base : `${base}?next=${encodeURIComponent(next)}`;
}

/** Visual chrome — centred stage with accent radial glow + faint grid mask. */
function LoginStage({ children, footMode }: { children: React.ReactNode; footMode: string | null }) {
  const { t } = useTranslation();
  const host = typeof window !== 'undefined' ? window.location.host : '';
  return (
    <div
      data-testid="login-stage"
      className="relative min-h-screen w-full grid place-items-center bg-bg-base px-6 py-6 overflow-hidden"
    >
      {/* accent radial glow */}
      <div
        aria-hidden
        data-testid="login-glow"
        className="pointer-events-none absolute left-1/2 -translate-x-1/2 -top-[30%] w-[780px] h-[560px] rounded-full"
        style={{
          background:
            'radial-gradient(closest-side, oklch(0.70 0.13 var(--accent-h) / 0.12), transparent 70%)',
        }}
      />
      {/* faint grid mask */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-50"
        style={{
          backgroundImage:
            'linear-gradient(oklch(1 0 0/.015) 1px,transparent 1px),linear-gradient(90deg,oklch(1 0 0/.015) 1px,transparent 1px)',
          backgroundSize: '34px 34px',
          WebkitMaskImage:
            'radial-gradient(closest-side at 50% 35%, #000, transparent 75%)',
          maskImage:
            'radial-gradient(closest-side at 50% 35%, #000, transparent 75%)',
        }}
      />
      <div className="relative z-[1] w-full flex items-center justify-center">{children}</div>
      <div
        data-testid="login-foot"
        className="absolute bottom-5 left-0 right-0 text-center text-[11.5px] text-tx-faint font-mono z-[1] px-4"
      >
        {footMode
          ? t('login.footTitle', { mode: footMode, host })
          : t('login.footTitleMinimal', { host })}
      </div>
    </div>
  );
}

function BrandRow({ subtitle }: { subtitle: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-[11px] text-center">
      <span
        data-testid="login-brand-tile"
        className="w-[46px] h-[46px] rounded-[13px] flex items-center justify-center text-accent-text"
        style={{
          background:
            'linear-gradient(150deg, var(--color-accent), var(--color-accent-strong))',
          boxShadow:
            '0 0 0 1px oklch(1 0 0/.08) inset, 0 6px 20px oklch(0.70 0.13 var(--accent-h) / 0.3)',
        }}
      >
        <img src={logoUrl} alt="" className="w-6 h-6" />
      </span>
      <div className="text-[21px] font-bold tracking-[-0.02em] leading-none">
        {t('app.name').toLowerCase()}
      </div>
      <div className="text-[12.5px] text-tx-faint -mt-1.5">{subtitle}</div>
    </div>
  );
}

function CardShell({ children }: { children: React.ReactNode }) {
  return (
    <div
      data-testid="login-card"
      className="w-[368px] max-w-full bg-bg-surface border border-border-subtle rounded-xl px-7 py-[30px] flex flex-col gap-[18px]"
      style={{ boxShadow: '0 28px 70px oklch(0 0 0 / 0.5)' }}
    >
      {children}
    </div>
  );
}

function InputPill({
  icon,
  children,
}: {
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-[9px] bg-bg-input border border-border-subtle rounded-md px-[11px] h-10">
      <span className="text-tx-faint shrink-0 flex items-center" aria-hidden>
        {icon}
      </span>
      {children}
    </div>
  );
}

function SsoButton({ href }: { href: string }) {
  const { t } = useTranslation();
  return (
    <Button asChild variant="outline" className="h-10 font-semibold gap-2">
      <a href={href} data-testid="oidc-login-link">
        <ShieldCheck className="w-4 h-4 text-tx-secondary" />
        {t('login.sso.button')}
      </a>
    </Button>
  );
}

function Divider() {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 text-tx-faint text-[11.5px]">
      <span className="flex-1 h-px bg-border-faint" />
      {t('login.or')}
      <span className="flex-1 h-px bg-border-faint" />
    </div>
  );
}

function modeLabel(cfg: AuthConfig | undefined, t: (k: string) => string): string | null {
  if (!cfg) return null;
  switch (cfg.mode) {
    case 'forms':
      return t('login.mode.forms');
    case 'oidc':
      return t('login.mode.oidc');
    case 'none':
      return t('login.mode.none');
    case 'basic':
      return t('login.mode.basic');
  }
}

export function Login() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const cfg = useAuthConfig();
  const session = useSession();
  const [serverErr, setServerErr] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  });

  // Mode-aware redirect: under mode=basic the browser popup handles auth
  // (stale-URL guard), AND if the user already has a valid session cookie
  // we send them home rather than render a redundant sign-in form (B-48).
  // The basic-mode clause is a load-bearing security invariant covered by
  // Login.test.tsx "redirects to / when mode=basic".
  useEffect(() => {
    const basicMode = cfg.isSuccess && cfg.data.mode === 'basic';
    if (basicMode || session.isSuccess) {
      navigate(safeNext(params.get('next')), { replace: true });
    }
  }, [cfg.isSuccess, cfg.data?.mode, session.isSuccess, navigate, params]);

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

  const foot = modeLabel(cfg.data, (k) => t(k));

  // Skeleton — avoid flashing the password form on a mode=oidc deployment.
  if (cfg.isPending) {
    return (
      <LoginStage footMode={null}>
        <CardShell>
          <BrandRow subtitle={t('login.brand.subtitle.loading')} />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </CardShell>
      </LoginStage>
    );
  }

  const next = safeNext(params.get('next'));
  const oidcReady = cfg.data?.oidcReady ?? false;

  // mode=oidc → SSO-only card.
  if (cfg.isSuccess && cfg.data.mode === 'oidc') {
    const href = ssoHref(cfg.data.loginUrl, next);
    return (
      <LoginStage footMode={foot}>
        <CardShell>
          <BrandRow subtitle={t('login.brand.subtitle.oidc')} />
          <p className="text-[13px] text-tx-secondary text-center leading-snug">
            {t('login.sso.intro')}
          </p>
          <SsoButton href={href} />
        </CardShell>
      </LoginStage>
    );
  }

  // mode=none → entry button (+ optional SSO button).
  if (cfg.isSuccess && cfg.data.mode === 'none') {
    return (
      <LoginStage footMode={foot}>
        <CardShell>
          <BrandRow subtitle={t('login.brand.subtitle.none')} />
          <Button asChild className="h-10 font-semibold gap-2">
            <a href={safeNext(params.get('next'))}>
              <ArrowRight className="w-4 h-4" />
              {t('login.enter')}
            </a>
          </Button>
          {oidcReady && (
            <>
              <Divider />
              <SsoButton href={ssoHref(cfg.data.loginUrl, next)} />
            </>
          )}
        </CardShell>
      </LoginStage>
    );
  }

  // Default branch: mode=forms (or error fallback) → password form.
  return (
    <LoginStage footMode={foot}>
      <CardShell>
        <BrandRow subtitle={t('login.brand.subtitle.forms')} />
        <form onSubmit={onSubmit} autoComplete="on" noValidate className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="username" className="text-[12px] text-tx-secondary font-medium">
              {t('login.username')}
            </Label>
            <InputPill icon={<User className="w-4 h-4" />}>
              <Input
                id="username"
                type="text"
                autoFocus
                autoComplete="username"
                placeholder={t('login.usernamePlaceholder')}
                aria-invalid={Boolean(errors.username) || undefined}
                className="border-0 bg-transparent p-0 h-auto text-[13.5px] focus-visible:ring-0 focus-visible:ring-offset-0"
                {...register('username')}
              />
            </InputPill>
            {errors.username && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {t(errors.username.message ?? 'login.usernameRequired')}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="password" className="text-[12px] text-tx-secondary font-medium">
              {t('login.password')}
            </Label>
            <InputPill icon={<KeyRound className="w-4 h-4" />}>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                placeholder={t('login.passwordPlaceholder')}
                aria-invalid={Boolean(errors.password || serverErr) || undefined}
                className="border-0 bg-transparent p-0 h-auto text-[13.5px] focus-visible:ring-0 focus-visible:ring-offset-0"
                {...register('password')}
              />
            </InputPill>
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

          <Button
            type="submit"
            disabled={isSubmitting}
            aria-label={t('login.submitButtonAria')}
            className="h-10 font-semibold gap-2 mt-1"
          >
            <LogIn className="w-4 h-4" />
            {isSubmitting ? t('login.submitting') : t('login.submit')}
          </Button>
        </form>

        {oidcReady && (
          <>
            <Divider />
            <SsoButton href={ssoHref(cfg.data?.loginUrl, next)} />
          </>
        )}
      </CardShell>
    </LoginStage>
  );
}
