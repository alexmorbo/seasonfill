import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError } from '@/lib/api';
import { login } from '@/lib/auth';

const schema = z.object({ api_key: z.string().min(1, 'API key required') });
type FormValues = z.infer<typeof schema>;

function safeNext(raw: string | null): string {
  if (!raw) return '/';
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/';
  return raw;
}

export function Login() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const [serverErr, setServerErr] = useState<string | null>(null);
  const { register, handleSubmit, formState: { errors, isSubmitting } } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { api_key: '' },
  });

  const onSubmit = handleSubmit(async ({ api_key }) => {
    setServerErr(null);
    try {
      await login(api_key);
      await qc.invalidateQueries({ queryKey: ['auth', 'session'] });
      navigate(safeNext(params.get('next')), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setServerErr('Invalid API key');
      else setServerErr(err instanceof Error ? err.message : 'Login failed');
    }
  });

  return (
    <div className="min-h-screen grid place-items-center bg-bg px-4">
      <form onSubmit={onSubmit} autoComplete="off"
        className="w-full max-w-sm bg-surface border border-border rounded-lg p-7 flex flex-col gap-5"
        noValidate>
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">seasonfill</h1>
          <p className="text-muted text-[13px] mt-1">Sonarr season helper</p>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="api_key" className="text-[12px] text-foreground-2">API Key</Label>
          <Input id="api_key" type="password" autoFocus autoComplete="off" placeholder="sf_•••••••••••••"
            aria-invalid={Boolean(errors.api_key || serverErr) || undefined}
            {...register('api_key')} />
          {errors.api_key && <p role="alert" className="text-status-danger text-[11.5px]">{errors.api_key.message}</p>}
          {serverErr && !errors.api_key && <p role="alert" className="text-status-danger text-[11.5px]">{serverErr}</p>}
        </div>
        <Button type="submit" disabled={isSubmitting} className="h-10 font-semibold">
          {isSubmitting ? 'Signing in…' : 'Sign in'}
        </Button>
        <p className="text-faint text-[11.5px] text-center">
          Need a key? See <span className="mono text-muted">helm/values.yaml</span> → <span className="mono">auth.apiKey</span>
        </p>
      </form>
    </div>
  );
}
