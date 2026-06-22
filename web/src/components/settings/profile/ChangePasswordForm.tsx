import { useTranslation } from 'react-i18next';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Info } from 'lucide-react';
import { useChangePassword } from '@/hooks/useMe';
import { ApiError } from '@/lib/api';

// Client-side minimum new-password length. Stricter than the BE's
// MinPasswordLen=8 (intentional — see story Decision §5). Operator
// gets earlier feedback for weak passwords.
const NEW_PASSWORD_MIN = 12;

const schema = z
  .object({
    current_password: z.string().min(1, 'settings.profile.change_password.current_required'),
    new_password: z
      .string()
      .min(NEW_PASSWORD_MIN, 'settings.profile.change_password.too_short'),
    confirm_password: z.string(),
  })
  .superRefine((v, ctx) => {
    if (v.new_password !== v.confirm_password) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['confirm_password'],
        message: 'settings.profile.change_password.mismatch',
      });
    }
  });

type FormValues = z.infer<typeof schema>;

// ChangePasswordForm is the forms-mode password change UI. Submits to
// POST /api/v1/me/change-password. On 204 success: clears the form
// and toasts. On 401: maps to current_password field error. On 405:
// renders a defensive disabled banner (the AuthSection should not have
// mounted this in non-forms mode, but if BE auth_mode flips mid-session
// the next submit will surface 405 — we want a clear message).
//
// Story 487 (N-7c).
export function ChangePasswordForm() {
  const { t } = useTranslation();
  const mut = useChangePassword();

  const {
    register,
    handleSubmit,
    reset,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { current_password: '', new_password: '', confirm_password: '' },
    mode: 'onBlur',
  });

  const onSubmit = handleSubmit((values) => {
    mut.mutate(
      { current_password: values.current_password, new_password: values.new_password },
      {
        onSuccess: () => {
          reset();
          toast.success(t('settings.profile.change_password.success'));
        },
        onError: (err) => {
          if (err instanceof ApiError) {
            if (err.status === 401) {
              setError('current_password', {
                message: 'settings.profile.change_password.wrong_current',
              });
              return;
            }
            if (err.status === 400 && /too_short|password_too_short/.test(err.message)) {
              setError('new_password', {
                message: 'settings.profile.change_password.too_short',
              });
              return;
            }
            // 405 fall-through handled by the unavailable banner; toast a
            // generic message so the operator still sees something. The
            // AuthSection branching makes 405 unreachable in practice.
            if (err.status === 405) {
              toast.error(t('settings.profile.change_password.unavailable_for_mode'));
              return;
            }
          }
          toast.error(t('settings.profile.change_password.generic_error'));
        },
      },
    );
  });

  // Defensive 405 banner — only paints if the mutation surfaced a 405
  // and the AuthSection somehow mounted us anyway. Normally unreachable.
  const unavailable =
    mut.error instanceof ApiError && mut.error.status === 405;

  if (unavailable) {
    return (
      <Alert>
        <Info className="w-4 h-4" />
        <AlertTitle>{t('settings.profile.change_password.unavailable_for_mode')}</AlertTitle>
        <AlertDescription>
          {t('settings.profile.change_password.unavailable_hint')}
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <form
      data-testid="change-password-form"
      onSubmit={onSubmit}
      className="flex flex-col gap-3"
      noValidate
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="cp-current">
          {t('settings.profile.change_password.current_label')}
        </Label>
        <Input
          id="cp-current"
          type="password"
          autoComplete="current-password"
          aria-invalid={Boolean(errors.current_password) || undefined}
          {...register('current_password')}
        />
        {errors.current_password && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(errors.current_password.message ?? '')}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="cp-new">
          {t('settings.profile.change_password.new_label')}
        </Label>
        <Input
          id="cp-new"
          type="password"
          autoComplete="new-password"
          aria-invalid={Boolean(errors.new_password) || undefined}
          {...register('new_password')}
        />
        <span className="text-[11.5px] text-muted">
          {t('settings.profile.change_password.min_hint', { n: NEW_PASSWORD_MIN })}
        </span>
        {errors.new_password && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(errors.new_password.message ?? '')}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="cp-confirm">
          {t('settings.profile.change_password.confirm_label')}
        </Label>
        <Input
          id="cp-confirm"
          type="password"
          autoComplete="new-password"
          aria-invalid={Boolean(errors.confirm_password) || undefined}
          {...register('confirm_password')}
        />
        {errors.confirm_password && (
          <p role="alert" className="text-status-danger text-[11.5px]">
            {t(errors.confirm_password.message ?? '')}
          </p>
        )}
      </div>

      <div className="flex items-center pt-1">
        <div className="flex-1" />
        <Button
          type="submit"
          disabled={isSubmitting || mut.isPending}
          data-testid="change-password-submit"
        >
          {isSubmitting || mut.isPending
            ? t('common.saving')
            : t('settings.profile.change_password.submit')}
        </Button>
      </div>
    </form>
  );
}
