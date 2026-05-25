import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError } from '@/lib/api';
import { loginWithPassword, sessionQueryKey } from '@/lib/auth';

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
  const [serverErr, setServerErr] = useState<string | null>(null);
  const { register, handleSubmit, formState: { errors, isSubmitting } } =
    useForm<FormValues>({
      resolver: zodResolver(schema),
      defaultValues: { username: '', password: '' },
    });

  const onSubmit = handleSubmit(async ({ username, password }) => {
    setServerErr(null);
    try {
      await loginWithPassword({ username, password });
      await qc.invalidateQueries({ queryKey: sessionQueryKey });
      navigate(safeNext(params.get('next')), { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401 || err.status === 429) {
          // 429 deliberately surfaces the same message as 401: a distinct
          // "rate-limited" copy would tell an attacker which usernames are
          // worth retrying.
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
