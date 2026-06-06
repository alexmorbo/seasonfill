import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Controller, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { CheckCircle2, Loader2, AlertTriangle, Wand2 } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  Tooltip, TooltipContent, TooltipProvider, TooltipTrigger,
} from '@/components/ui/tooltip';

import { TagListEditor } from './instance-form-fields';
import {
  useDiscoverQbit,
  useInstallWebhook,
  useQbitSettings,
  useUpsertQbitSettings,
  type QbitSettingsDTO,
  type QbitSettingsUpsertRequest,
} from '@/api/qbit';
import { ApiError } from '@/lib/api';

// Defaults mirror application/regrab/settings_usecase.go validation
// bounds and the documented "happy path" recommended values.
const DEFAULTS = {
  url: 'http://qbittorrent:8080',
  username: '',
  password: '',
  category: 'sonarr',
  poll_interval_minutes: 30,
  regrab_cooldown_hours: 120,
  max_consecutive_no_better: 3,
  custom_unregistered_msgs: [] as string[],
  enabled: false,
} as const;

// Zod schema — message strings are i18n keys; the render layer
// passes them through `t()` directly. Number bounds match 039d AC-8.
const schema = z.object({
  url: z
    .string()
    .min(1, 'settings.instances.form.watchdog.errors.urlRequired')
    .url('settings.instances.form.watchdog.errors.urlInvalid')
    .refine(
      (v) => v.startsWith('http://') || v.startsWith('https://'),
      'settings.instances.form.watchdog.errors.urlInvalid',
    ),
  username: z.string().max(256),
  password: z.string().max(512),
  category: z
    .string()
    .min(1, 'settings.instances.form.watchdog.errors.categoryRequired')
    .max(64),
  poll_interval_minutes: z
    .number()
    .int()
    .min(5, 'settings.instances.form.watchdog.errors.pollIntervalRange')
    .max(1440, 'settings.instances.form.watchdog.errors.pollIntervalRange'),
  regrab_cooldown_hours: z
    .number()
    .int()
    .min(1, 'settings.instances.form.watchdog.errors.cooldownRange')
    .max(720, 'settings.instances.form.watchdog.errors.cooldownRange'),
  max_consecutive_no_better: z
    .number()
    .int()
    .min(1, 'settings.instances.form.watchdog.errors.consecutiveRange')
    .max(100, 'settings.instances.form.watchdog.errors.consecutiveRange'),
  custom_unregistered_msgs: z
    .array(z.string().min(3).max(200))
    .max(100, 'settings.instances.form.watchdog.errors.tooManyMsgs'),
  enabled: z.boolean(),
});

type FormValues = z.infer<typeof schema>;

// Build FormValues from a server DTO. Password ALWAYS starts blank —
// the server returns `password_set` only; we never round-trip the
// plaintext through the browser (AC-10).
function formFromDTO(dto: QbitSettingsDTO | null): FormValues {
  if (!dto) return { ...DEFAULTS, custom_unregistered_msgs: [] };
  return {
    url: dto.url ?? DEFAULTS.url,
    username: dto.username ?? '',
    password: '',
    category: dto.category ?? DEFAULTS.category,
    poll_interval_minutes:
      dto.poll_interval_minutes ?? DEFAULTS.poll_interval_minutes,
    regrab_cooldown_hours:
      dto.regrab_cooldown_hours ?? DEFAULTS.regrab_cooldown_hours,
    max_consecutive_no_better:
      dto.max_consecutive_no_better ?? DEFAULTS.max_consecutive_no_better,
    custom_unregistered_msgs: [...(dto.custom_unregistered_msgs ?? [])],
    enabled: Boolean(dto.enabled),
  };
}

// Build the wire payload. Empty password = preserve existing
// ciphertext (dirty-bit) per 039d AC-4.
function valuesToPayload(v: FormValues): QbitSettingsUpsertRequest {
  return {
    url: v.url.trim(),
    username: v.username.trim(),
    password: v.password,
    category: v.category.trim(),
    poll_interval_minutes: v.poll_interval_minutes,
    regrab_cooldown_hours: v.regrab_cooldown_hours,
    max_consecutive_no_better: v.max_consecutive_no_better,
    custom_unregistered_msgs: [...v.custom_unregistered_msgs],
    enabled: v.enabled,
  };
}

export interface WatchdogTabProps {
  readonly instanceName: string;
}

export function WatchdogTab({ instanceName }: WatchdogTabProps) {
  const { t } = useTranslation();

  const settingsQuery = useQbitSettings(instanceName);
  const upsert = useUpsertQbitSettings(instanceName);
  const installWebhook = useInstallWebhook(instanceName);

  const [discoverEnabled, setDiscoverEnabled] = useState(false);
  const discoverQuery = useDiscoverQbit(instanceName, { enabled: discoverEnabled });

  // The banner-side "installed" signal: starts off the settings query
  // (a row exists OR `enabled` is true ⇒ webhook must have been
  // installed at some point) and flips true on a successful install
  // mutation. Note that a fresh instance with no settings cannot
  // distinguish "webhook installed but no settings yet" from "neither"
  // — the operator clicks Install once and the banner sticks.
  const initialInstalled =
    Boolean(settingsQuery.data?.enabled) ||
    Boolean(settingsQuery.data && settingsQuery.data.url);
  const [webhookInstalled, setWebhookInstalled] = useState(initialInstalled);
  useEffect(() => {
    if (initialInstalled) setWebhookInstalled(true);
  }, [initialInstalled]);
  useEffect(() => {
    if (installWebhook.isSuccess) setWebhookInstalled(true);
  }, [installWebhook.isSuccess]);

  const passwordSet = Boolean(settingsQuery.data?.password_set);

  const {
    register, handleSubmit, control, reset, setValue, watch, setError,
    formState: { errors, isDirty, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: formFromDTO(null),
    mode: 'onBlur',
  });

  // Re-seed whenever settings arrive / change AND the form is pristine
  // (don't blow away user edits on a background refetch).
  useEffect(() => {
    if (!settingsQuery.data && !settingsQuery.isPending) {
      // 404 / null — render defaults (only on first paint).
      if (!isDirty) reset(formFromDTO(null));
      return;
    }
    if (settingsQuery.data && !isDirty) {
      reset(formFromDTO(settingsQuery.data));
    }
    // We intentionally exclude `isDirty` from the dep array — flipping
    // it should not re-seed the form, only the underlying data should.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settingsQuery.data, settingsQuery.isPending, reset]);

  // Auto-fill from Sonarr → on success populate url/username/category.
  // Password is deliberately not touched (Sonarr redacts it).
  useEffect(() => {
    if (discoverQuery.isSuccess && discoverQuery.data) {
      const d = discoverQuery.data;
      if (d.url) setValue('url', d.url, { shouldDirty: true });
      if (d.username !== undefined) {
        setValue('username', d.username, { shouldDirty: true });
      }
      if (d.category) setValue('category', d.category, { shouldDirty: true });
      // Reset the trigger so a second click re-fires (the query is
      // keyed on instanceName so a re-mount isn't enough on its own).
      setDiscoverEnabled(false);
    }
  }, [discoverQuery.isSuccess, discoverQuery.data, setValue]);

  useEffect(() => {
    if (discoverQuery.isError && discoverQuery.error) {
      const err = discoverQuery.error;
      // 404 NO_QBIT_FOUND is by far the most likely failure — the
      // backend matches the parent Sonarr's downloadclient list
      // against "qbittorrent" and 404s when none is found.
      const code =
        typeof err.body === 'object' && err.body !== null && 'code' in err.body
          ? (err.body as { code?: string }).code
          : '';
      if (err.status === 404 || code === 'NO_QBIT_FOUND') {
        // Toast is sufficient here; the form stays as-is.
        // (Imported lazily via i18n to avoid extra imports.)
      }
      setDiscoverEnabled(false);
    }
  }, [discoverQuery.isError, discoverQuery.error]);

  const onSubmit = handleSubmit(async (values) => {
    const body = valuesToPayload(values);
    try {
      await upsert.mutateAsync({ body });
      // Reset dirty state to the new server-side truth: the saved
      // password (if any) is now persisted, so the input must clear
      // and the placeholder must update on the next render via the
      // query refetch invalidation.
      reset({ ...values, password: '' });
    } catch (err) {
      // 400 → map field-specific errors back to RHF when the body
      // includes a `field` discriminator (matches the backend
      // BAD_REQUEST envelope from 039d AC-8).
      if (err instanceof ApiError && err.status === 400) {
        const body = err.body as { code?: string; field?: string; error?: string } | undefined;
        if (body?.field && body.field in values) {
          setError(body.field as keyof FormValues, {
            type: 'server',
            message:
              body.error ??
              'settings.instances.form.watchdog.errors.serverValidation',
          });
        }
      }
      // Toast already fired by the hook's onError — don't double up.
    }
  });

  // The Switch lock is purely a UX guardrail; the backend will
  // independently 409 when enabled=true is sent without a webhook.
  const enableLocked = !webhookInstalled;

  // Convenience: localised error message per RHF field.
  const fieldErr = (key: keyof FormValues): string | undefined => {
    const e = errors[key];
    if (!e?.message) return undefined;
    return t(e.message as string, { defaultValue: e.message as string });
  };

  const passwordValue = watch('password');
  const passwordPlaceholder = useMemo(() => {
    if (passwordSet && passwordValue === '') {
      return t('settings.instances.form.watchdog.form.password.placeholderSet');
    }
    return t('settings.instances.form.watchdog.form.password.placeholderUnset');
  }, [passwordSet, passwordValue, t]);

  return (
    <div className="flex flex-col gap-4">
      {/* ----------- Section A: webhook gate banner ---------- */}
      {webhookInstalled ? (
        <Alert
          data-testid="watchdog-webhook-installed"
          className="border-status-ok/50"
        >
          <CheckCircle2 className="h-4 w-4 text-status-ok" />
          <AlertTitle>
            {t('settings.instances.form.watchdog.webhookGate.installed')}
          </AlertTitle>
        </Alert>
      ) : (
        <Alert data-testid="watchdog-webhook-gate" variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>
            {t('settings.instances.form.watchdog.webhookGate.notInstalled')}
          </AlertTitle>
          <AlertDescription className="mt-2 flex items-center gap-3">
            <Button
              type="button"
              size="sm"
              onClick={() => installWebhook.mutate()}
              disabled={installWebhook.isPending}
            >
              {installWebhook.isPending && (
                <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />
              )}
              {t('settings.instances.form.watchdog.webhookGate.installBtn')}
            </Button>
            {installWebhook.isError && installWebhook.error.status === 412 && (
              <a
                href="/settings#webhooks"
                className="text-[12px] underline underline-offset-2"
                data-testid="watchdog-public-url-link"
              >
                {t('settings.instances.form.watchdog.webhookGate.publicUrlMissing')}
              </a>
            )}
          </AlertDescription>
        </Alert>
      )}

      {/* ----------- Section B: settings form ---------------- */}
      <form
        onSubmit={onSubmit}
        className="flex flex-col gap-4"
        noValidate
        data-testid="watchdog-form"
      >
        {/* Action row (Auto-fill + Save) -------------------- */}
        <div className="flex items-center justify-between gap-3">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setDiscoverEnabled(true)}
            disabled={discoverQuery.isFetching}
            className="gap-1.5"
          >
            {discoverQuery.isFetching ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <Wand2 className="w-3.5 h-3.5" />
            )}
            {t('settings.instances.form.watchdog.actions.autoFill')}
          </Button>
          <Button
            type="submit"
            size="sm"
            disabled={!isDirty || isSubmitting || upsert.isPending}
          >
            {(isSubmitting || upsert.isPending) && (
              <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />
            )}
            {t('settings.instances.form.watchdog.actions.save')}
          </Button>
        </div>

        {/* URL ---------------------------------------------- */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="qbit-url">
            {t('settings.instances.form.watchdog.form.url.label')}
          </Label>
          <Input
            id="qbit-url"
            type="url"
            placeholder={t('settings.instances.form.watchdog.form.url.placeholder')}
            aria-invalid={Boolean(errors.url) || undefined}
            {...register('url')}
          />
          <p className="text-[11.5px] text-muted">
            {t('settings.instances.form.watchdog.form.url.help')}
          </p>
          {fieldErr('url') && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {fieldErr('url')}
            </p>
          )}
        </div>

        <div className="grid grid-cols-2 gap-4">
          {/* Username ------------------------------------- */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="qbit-user">
              {t('settings.instances.form.watchdog.form.username.label')}
            </Label>
            <Input
              id="qbit-user"
              autoComplete="off"
              aria-invalid={Boolean(errors.username) || undefined}
              {...register('username')}
            />
            <p className="text-[11.5px] text-muted">
              {t('settings.instances.form.watchdog.form.username.help')}
            </p>
          </div>

          {/* Password ------------------------------------- */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="qbit-pass">
              {t('settings.instances.form.watchdog.form.password.label')}
            </Label>
            <Input
              id="qbit-pass"
              type="password"
              autoComplete="new-password"
              placeholder={passwordPlaceholder}
              aria-invalid={Boolean(errors.password) || undefined}
              {...register('password')}
            />
            <p className="text-[11.5px] text-muted">
              {t('settings.instances.form.watchdog.form.password.help')}
            </p>
          </div>
        </div>

        {/* Category ----------------------------------------- */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="qbit-cat">
            {t('settings.instances.form.watchdog.form.category.label')}
          </Label>
          <Input
            id="qbit-cat"
            aria-invalid={Boolean(errors.category) || undefined}
            {...register('category')}
          />
          <p className="text-[11.5px] text-muted">
            {t('settings.instances.form.watchdog.form.category.help')}
          </p>
          {fieldErr('category') && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {fieldErr('category')}
            </p>
          )}
        </div>

        {/* Numeric trio (poll / cooldown / strikes) -------- */}
        <div className="grid grid-cols-3 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="qbit-poll">
              {t('settings.instances.form.watchdog.form.pollInterval.label')}
            </Label>
            <Controller
              control={control}
              name="poll_interval_minutes"
              render={({ field }) => (
                <Input
                  id="qbit-poll"
                  type="number"
                  inputMode="numeric"
                  min={5}
                  max={1440}
                  value={field.value as number | string}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors.poll_interval_minutes) || undefined}
                />
              )}
            />
            <p className="text-[11.5px] text-muted">
              {t('settings.instances.form.watchdog.form.pollInterval.help')}
            </p>
            {fieldErr('poll_interval_minutes') && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {fieldErr('poll_interval_minutes')}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="qbit-cd">
              {t('settings.instances.form.watchdog.form.regrabCooldown.label')}
            </Label>
            <Controller
              control={control}
              name="regrab_cooldown_hours"
              render={({ field }) => (
                <Input
                  id="qbit-cd"
                  type="number"
                  inputMode="numeric"
                  min={1}
                  max={720}
                  value={field.value as number | string}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors.regrab_cooldown_hours) || undefined}
                />
              )}
            />
            <p className="text-[11.5px] text-muted">
              {t('settings.instances.form.watchdog.form.regrabCooldown.help')}
            </p>
            {fieldErr('regrab_cooldown_hours') && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {fieldErr('regrab_cooldown_hours')}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="qbit-strike">
              {t('settings.instances.form.watchdog.form.maxConsecutive.label')}
            </Label>
            <Controller
              control={control}
              name="max_consecutive_no_better"
              render={({ field }) => (
                <Input
                  id="qbit-strike"
                  type="number"
                  inputMode="numeric"
                  min={1}
                  max={100}
                  value={field.value as number | string}
                  onChange={(e) => field.onChange(Number(e.target.value))}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors.max_consecutive_no_better) || undefined}
                />
              )}
            />
            <p className="text-[11.5px] text-muted">
              {t('settings.instances.form.watchdog.form.maxConsecutive.help')}
            </p>
            {fieldErr('max_consecutive_no_better') && (
              <p role="alert" className="text-status-danger text-[11.5px]">
                {fieldErr('max_consecutive_no_better')}
              </p>
            )}
          </div>
        </div>

        {/* Custom unregistered messages -------------------- */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="qbit-custom-msgs">
            {t('settings.instances.form.watchdog.form.customMsgs.label')}
          </Label>
          <Controller
            control={control}
            name="custom_unregistered_msgs"
            render={({ field }) => (
              <TagListEditor
                id="qbit-custom-msgs"
                value={field.value}
                onChange={(next) => field.onChange([...next])}
                placeholder={t(
                  'settings.instances.form.watchdog.form.customMsgs.addPlaceholder',
                )}
              />
            )}
          />
          <p className="text-[11.5px] text-muted">
            {t('settings.instances.form.watchdog.form.customMsgs.help')}
          </p>
          {fieldErr('custom_unregistered_msgs') && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {fieldErr('custom_unregistered_msgs')}
            </p>
          )}
        </div>

        {/* Enabled Switch ---------------------------------- */}
        <div className="flex items-start gap-3 pt-1">
          <Controller
            control={control}
            name="enabled"
            render={({ field }) => (
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="inline-flex">
                      <Switch
                        id="qbit-enabled"
                        checked={Boolean(field.value)}
                        onCheckedChange={(v) => field.onChange(v)}
                        disabled={enableLocked}
                        aria-label={t(
                          'settings.instances.form.watchdog.form.enabled.label',
                        )}
                      />
                    </span>
                  </TooltipTrigger>
                  {enableLocked && (
                    <TooltipContent>
                      {t(
                        'settings.instances.form.watchdog.form.enabled.helpDisabled',
                      )}
                    </TooltipContent>
                  )}
                </Tooltip>
              </TooltipProvider>
            )}
          />
          <div className="flex flex-col gap-0.5">
            <Label htmlFor="qbit-enabled" className="font-normal">
              {t('settings.instances.form.watchdog.form.enabled.label')}
            </Label>
            <p className="text-[11.5px] text-muted">
              {enableLocked
                ? t('settings.instances.form.watchdog.form.enabled.helpDisabled')
                : t('settings.instances.form.watchdog.form.enabled.helpEnabled')}
            </p>
          </div>
        </div>
      </form>
    </div>
  );
}
