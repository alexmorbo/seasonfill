import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { KeyRound, Loader2 } from 'lucide-react';
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import {
  Tooltip, TooltipContent, TooltipProvider, TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  useCreateInstance,
  useInstanceDetail,
  useTestInstance,
  useUpdateInstance,
  type InstanceCreateRequest,
  type InstanceUpdateRequest,
} from '@/lib/instances-mutations';

const schema = z.object({
  name: z
    .string()
    .min(1, 'Name is required')
    .max(128, 'Max 128 characters')
    .regex(/^[a-zA-Z0-9_-]+$/, 'Allowed: a-z, A-Z, 0-9, _ and -'),
  url: z
    .string()
    .min(1, 'URL is required')
    .url('Must be a valid URL')
    .refine((v) => v.startsWith('http://') || v.startsWith('https://'),
      'URL must start with http:// or https://'),
  api_key: z.string(),
  mode: z.enum(['auto', 'manual']),
});
type FormValues = z.infer<typeof schema>;

export interface InstanceFormDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (v: boolean) => void;
  readonly mode: 'create' | 'edit';
  readonly initial?: Partial<FormValues> | undefined;
}

const DEFAULTS: FormValues = {
  name: '',
  url: 'http://sonarr:8989',
  api_key: '',
  mode: 'auto',
};

export function InstanceFormDialog({
  open, onOpenChange, mode, initial,
}: InstanceFormDialogProps) {
  const isEdit = mode === 'edit';
  const create = useCreateInstance();
  const update = useUpdateInstance();
  const probe = useTestInstance();
  const [probeResult, setProbeResult] = useState<string | null>(null);

  // In edit mode we MUST merge the form values onto the full instance
  // detail before PUT — otherwise GORM-side full-replace silently loses
  // cooldown / ranking / limits / tags / retry / etc. The hook is keyed
  // by name; it's disabled in create mode (name=null), so no wasted
  // fetch.
  const detailQuery = useInstanceDetail(isEdit ? (initial?.name ?? null) : null);
  const detail = detailQuery.data?.detail;

  const {
    register, handleSubmit, reset, getValues,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { ...DEFAULTS, ...initial, api_key: '' },
    mode: 'onBlur',
  });

  useEffect(() => {
    if (open) {
      reset({ ...DEFAULTS, ...initial, api_key: '' });
      setProbeResult(null);
    }
  }, [open, initial, reset]);

  const onSubmit = handleSubmit(async (values) => {
    const trimmedKey = values.api_key.trim();

    if (isEdit && initial?.name) {
      // Wait for detail; the submit button is disabled while pending,
      // but guard here too for the race between click and click-handler
      // dispatch.
      if (!detail) return;
      // Merge: detail provides every per-instance field (cooldown,
      // ranking, limits, retry, search, tags, health_check, rate-limit,
      // timeouts, dry_run); form values overlay the 4 editable ones.
      // api_key omitted unless the user typed something — server then
      // preserves the existing secret.
      const body: InstanceUpdateRequest = {
        ...detail,
        name: values.name,
        url: values.url,
        mode: values.mode,
        ...(trimmedKey.length > 0 ? { api_key: trimmedKey } : {}),
      };
      // `updated_at` is read-only echo from GET; strip so it doesn't
      // round-trip into the request body. The handler ignores unknown
      // fields, but cleaner over the wire.
      delete (body as { updated_at?: string }).updated_at;
      await update.mutateAsync({ name: initial.name, body });
    } else {
      // Create path is patch-like, build from form alone. api_key is
      // required on create.
      if (trimmedKey.length === 0) return;
      const body: InstanceCreateRequest = {
        name: values.name,
        url: values.url,
        api_key: trimmedKey,
        mode: values.mode,
      };
      await create.mutateAsync({ body });
    }
    onOpenChange(false);
  });

  const onTest = async () => {
    setProbeResult(null);
    const { url, api_key } = getValues();
    if (!url || !api_key) {
      setProbeResult('URL and api_key are required to test');
      return;
    }
    try {
      const resp = await probe.mutateAsync({ url, api_key });
      if (resp.ok) {
        setProbeResult(resp.version
          ? `OK — Sonarr ${resp.version}`
          : 'OK — Sonarr (version unknown)');
      } else {
        setProbeResult(resp.reason || 'Connection failed');
      }
    } catch {
      setProbeResult(null);
    }
  };

  const editBlocked = isEdit && (detailQuery.isPending || detailQuery.isError || !detail);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? 'Edit instance' : 'Add Sonarr instance'}</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="inst-name">Name</Label>
            <Input
              id="inst-name"
              autoFocus={!isEdit}
              disabled={isEdit}
              aria-invalid={Boolean(errors.name) || undefined}
              {...register('name')}
            />
            {isEdit && (
              <p className="text-[11.5px] text-muted">
                Name is immutable. Delete and recreate to rename.
              </p>
            )}
            {errors.name && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.name.message}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="inst-url">URL</Label>
            <Input
              id="inst-url"
              type="url"
              aria-invalid={Boolean(errors.url) || undefined}
              {...register('url')}
            />
            {errors.url && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {errors.url.message}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <Label htmlFor="inst-key">API key</Label>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="secondary" className="gap-1 text-[10.5px]">
                      <KeyRound className="w-3 h-3" />
                      Encrypted at rest
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent>
                    Stored AES-256-GCM with a key derived per-row via HKDF.
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>
            <Input
              id="inst-key"
              type="password"
              autoComplete="off"
              placeholder={isEdit ? 'Leave empty to keep current key' : ''}
              aria-invalid={Boolean(errors.api_key) || undefined}
              {...register('api_key')}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="inst-mode">Mode</Label>
            <select
              id="inst-mode"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              {...register('mode')}
            >
              <option value="auto">auto</option>
              <option value="manual">manual</option>
            </select>
          </div>

          <div className="flex items-center gap-3">
            <Button
              type="button"
              variant="outline"
              onClick={onTest}
              disabled={probe.isPending}
            >
              {probe.isPending && <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />}
              Test connection
            </Button>
            {probeResult && (
              <span role="status" className="text-[12px] text-foreground-2">
                {probeResult}
              </span>
            )}
          </div>

          {isEdit && detailQuery.isPending && (
            <p className="text-[11.5px] text-muted flex items-center gap-1.5">
              <Loader2 className="w-3 h-3 animate-spin" />
              Loading instance details…
            </p>
          )}
          {isEdit && detailQuery.isError && (
            <p role="alert" className="text-[11.5px] text-status-danger">
              Could not load instance details. Close and retry to avoid
              overwriting per-instance settings.
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSubmitting || editBlocked}>
              {isSubmitting ? 'Saving…' : isEdit ? 'Save' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
