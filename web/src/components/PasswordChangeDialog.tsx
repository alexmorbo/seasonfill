import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError } from '@/lib/api';
import { changePassword, sessionQueryKey } from '@/lib/auth';

// Mirror backend `auth.MinPasswordLen` (021a-1a) — 8 chars.
const MIN_LEN = 8;

const schema = z
  .object({
    current: z.string().min(1, 'Current password required'),
    newPassword: z.string().min(MIN_LEN, `Min ${MIN_LEN} characters`),
    confirmNew: z.string().min(1, 'Confirm new password'),
  })
  .refine((v) => v.newPassword === v.confirmNew, {
    path: ['confirmNew'],
    message: 'Passwords do not match',
  });
type FormValues = z.infer<typeof schema>;

export function PasswordChangeDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (next: boolean) => void;
}) {
  const qc = useQueryClient();
  const [serverErr, setServerErr] = useState<string | null>(null);
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } =
    useForm<FormValues>({
      resolver: zodResolver(schema),
      defaultValues: { current: '', newPassword: '', confirmNew: '' },
    });

  // Reset form + error every time the dialog closes.
  useEffect(() => {
    if (!open) {
      reset({ current: '', newPassword: '', confirmNew: '' });
      setServerErr(null);
    }
  }, [open, reset]);

  const mutation = useMutation({
    mutationFn: (input: { current: string; newPassword: string }) => changePassword(input),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: sessionQueryKey });
      toast.success('Password updated');
      onOpenChange(false);
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.status === 401) setServerErr('Current password is incorrect');
        else if (err.status === 400) setServerErr(err.message || 'Password does not meet requirements');
        else setServerErr(err.message || 'Failed to update password');
      } else {
        setServerErr(err instanceof Error ? err.message : 'Failed to update password');
      }
    },
  });

  const onSubmit = handleSubmit(({ current, newPassword }) => {
    setServerErr(null);
    mutation.mutate({ current, newPassword });
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change password</DialogTitle>
          <DialogDescription>
            Set a new password for this seasonfill account.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4" autoComplete="off" noValidate>
          <div className="flex flex-col gap-2">
            <Label htmlFor="pwc-current">Current password</Label>
            <Input
              id="pwc-current"
              type="password"
              autoComplete="current-password"
              aria-invalid={Boolean(errors.current) || undefined}
              {...register('current')}
            />
            {errors.current && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.current.message}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="pwc-new">New password</Label>
            <Input
              id="pwc-new"
              type="password"
              autoComplete="new-password"
              aria-invalid={Boolean(errors.newPassword) || undefined}
              {...register('newPassword')}
            />
            {errors.newPassword && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.newPassword.message}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="pwc-confirm">Confirm new password</Label>
            <Input
              id="pwc-confirm"
              type="password"
              autoComplete="new-password"
              aria-invalid={Boolean(errors.confirmNew) || undefined}
              {...register('confirmNew')}
            />
            {errors.confirmNew && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.confirmNew.message}
              </p>
            )}
          </div>

          {serverErr && (
            <p role="alert" className="text-status-danger text-[12px]">{serverErr}</p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSubmitting || mutation.isPending}>
              {mutation.isPending ? 'Saving…' : 'Update password'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
