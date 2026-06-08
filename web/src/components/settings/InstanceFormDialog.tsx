import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useForm, useWatch, type FieldErrors } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { Cable, ShieldCheck, SlidersHorizontal } from 'lucide-react';
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Accordion } from '@/components/ui/accordion';
import {
  useInstanceDetail,
  useSaveInstanceWithQbit,
  useTestInstance,
  type InstanceCreateRequest,
  type InstanceUpdateRequest,
  type InstanceDetail,
} from '@/lib/instances-mutations';
import { DtoInstanceCooldownMode, DtoInstanceTagsMode } from '@/api/schema';
import {
  useQbitSettings,
  type QbitSettingsDTO,
  type QbitSettingsUpsertRequest,
} from '@/api/qbit';
import {
  FORM_DEFAULTS, WATCHDOG_DEFAULTS,
  dryRunFromWire, dryRunToWire, type DryRunChoice,
} from './instance-form-helpers';
import { WatchdogSection } from '@/components/instances/form/WatchdogSection';
import { PromotedControls } from '@/components/instances/form/PromotedControls';
import { ConnectionSection } from '@/components/instances/form/ConnectionSection';
import { TuningSection } from '@/components/instances/form/TuningSection';
import { AccordionSection } from '@/components/instances/form/AccordionSection';
import { DirtyFooter } from '@/components/instances/form/DirtyFooter';

const nameRule = z
  .string()
  .min(1, 'settings.instances.form.errors.nameRequired')
  .max(128, 'settings.instances.form.errors.nameTooLong')
  .regex(/^[a-zA-Z0-9_-]+$/, 'settings.instances.form.errors.nameRegex');

const urlRule = z
  .string()
  .min(1, 'settings.instances.form.errors.urlRequired')
  .url('settings.instances.form.errors.urlInvalid')
  .refine((v) => v.startsWith('http://') || v.startsWith('https://'),
    'settings.instances.form.errors.urlScheme');

const urlOrEmptyRule = z
  .string()
  .refine((v) => v === '' || /^https?:\/\//.test(v),
    'settings.instances.form.errors.urlScheme')
  .refine((v) => {
    if (v === '') return true;
    try { new URL(v); return true; } catch { return false; }
  }, 'settings.instances.form.errors.urlInvalid')
  .refine((v) => !v.endsWith('/'),
    'settings.instances.form.errors.urlTrailingSlash');

const modeRule = z.enum(['auto', 'manual']);
const dryRunRule = z.enum(['auto', 'on', 'off']);
const tagsModeRule = z.enum(['off', 'include', 'exclude', 'both']);
const cooldownModeRule = z.enum(['smart', 'strict']);

const fieldLabelKey = (technicalLabel: string) =>
  `settings.instances.form.fieldLabels.${technicalLabel}`;

type NumberErrKind = 'integer' | 'min' | 'max';
const numberErrKey: Record<NumberErrKind, string> = {
  integer: 'settings.instances.form.errors.numberMustBeInteger',
  min:     'settings.instances.form.errors.numberMin',
  max:     'settings.instances.form.errors.numberMax',
};
const numMsg = (kind: NumberErrKind, label: string, value?: number) =>
  JSON.stringify({
    i18n: numberErrKey[kind],
    params: { label: fieldLabelKey(label), ...(value !== undefined ? { value } : {}) },
  });

const int = (min: number, max: number, label: string) =>
  z.number()
    .int(numMsg('integer', label))
    .min(min, numMsg('min', label, min))
    .max(max, numMsg('max', label, max));
const float = (min: number, max: number, label: string) =>
  z.number()
    .min(min, numMsg('min', label, min))
    .max(max, numMsg('max', label, max));

function tValidationError(msg: string | undefined, t: TFunction): string {
  if (!msg) return '';
  try {
    const parsed = JSON.parse(msg) as { i18n?: string; params?: Record<string, unknown> };
    if (parsed?.i18n) {
      const params: Record<string, string | number> = {};
      for (const [k, v] of Object.entries(parsed.params ?? {})) {
        if (typeof v === 'string' || typeof v === 'number') params[k] = v;
      }
      if (typeof params.label === 'string'
        && params.label.startsWith('settings.instances.form.fieldLabels.')) {
        params.label = t(params.label);
      }
      return t(parsed.i18n, params);
    }
  } catch {
    // legacy plain-key path
  }
  return t(msg, { defaultValue: msg });
}

const baseShape = {
  name: nameRule,
  url: urlRule,
  public_url: urlOrEmptyRule,
  webhook_install_enabled: z.boolean(),
  webhook_url_override: urlOrEmptyRule,
  mode: modeRule,
  dry_run: dryRunRule,
  timeout_sec: int(1, 300, 'timeout_sec'),
  search_timeout_sec: int(1, 600, 'search_timeout_sec'),
  tags_mode: tagsModeRule,
  tags_include: z.array(z.string().min(1).max(64)),
  tags_exclude: z.array(z.string().min(1).max(64)),
  search_require_all_aired: z.boolean(),
  search_skip_specials: z.boolean(),
  search_skip_anime: z.boolean(),
  search_min_custom_format_score: int(-1000, 1000, 'min_custom_format_score'),
  ranking_indexer_priority_enabled: z.boolean(),
  ranking_origin_bonus: float(-100, 100, 'origin_bonus'),
  rate_limit_rpm: int(0, 10000, 'rate_limit_rpm'),
  rate_limit_burst: int(0, 10000, 'rate_limit_burst'),
  limits_scan_max_series: int(0, 100000, 'scan_max_series'),
  limits_max_grabs_per_scan: int(0, 100, 'max_grabs_per_scan'),
  cooldown_mode: cooldownModeRule,
  cooldown_series_after_grab_sec: int(0, 604800, 'cooldown_series_after_grab'),
  cooldown_guid_after_failed_grab_sec: int(0, 604800, 'cooldown_guid_after_failed_grab'),
  cooldown_guid_after_failed_import_sec: int(0, 604800, 'cooldown_guid_after_failed_import'),
  retry_max_attempts: int(0, 10, 'retry_max_attempts'),
  retry_initial_backoff_sec: int(0, 3600, 'retry_initial_backoff'),
  retry_max_backoff_sec: int(0, 3600, 'retry_max_backoff'),
  health_recheck_auth_sec: int(10, 86400, 'health_recheck_auth'),
  health_recheck_network_sec: int(10, 86400, 'health_recheck_network'),
};

const qbitShape = {
  qbit_url: z.string().refine(
    (v) => v === '' || /^https?:\/\//.test(v),
    'settings.instances.form.watchdog.errors.urlInvalid',
  ),
  // 083 / F-P2-1: optional browser-reachable URL. Mirrors qbit_url's
  // empty-OK + http(s)-only rule; backend accepts '' to clear.
  qbit_public_url: z.string().refine(
    (v) => v === '' || /^https?:\/\//.test(v),
    'settings.instances.form.watchdog.errors.urlInvalid',
  ),
  qbit_username: z.string().max(256),
  qbit_password: z.string().max(512),
  qbit_category: z.string().max(64),
  qbit_poll_interval_minutes: z.number().int().min(5).max(1440),
  qbit_regrab_cooldown_hours: z.number().int().min(1).max(720),
  qbit_max_consecutive_no_better: z.number().int().min(1).max(100),
  qbit_custom_unregistered_msgs: z.array(z.string().min(3).max(200)).max(100),
  qbit_enabled: z.boolean(),
};
const createSchema = z.object({
  ...baseShape, ...qbitShape,
  api_key: z.string().min(1, 'settings.instances.form.errors.apiKeyRequiredCreate'),
});
const editSchema = z.object({ ...baseShape, ...qbitShape, api_key: z.string() });
type FormValues = z.infer<typeof createSchema>;
const pickSchema = (m: 'create' | 'edit') => (m === 'create' ? createSchema : editSchema);

function qbitFromDTO(dto: QbitSettingsDTO | null | undefined): Partial<FormValues> {
  if (!dto) return { ...WATCHDOG_DEFAULTS };
  return {
    qbit_url: dto.url ?? WATCHDOG_DEFAULTS.qbit_url,
    qbit_public_url: dto.qbit_public_url ?? '',
    qbit_username: dto.username ?? '',
    qbit_password: '', // dirty-bit invariant
    qbit_category: dto.category ?? WATCHDOG_DEFAULTS.qbit_category,
    qbit_poll_interval_minutes: dto.poll_interval_minutes ?? WATCHDOG_DEFAULTS.qbit_poll_interval_minutes,
    qbit_regrab_cooldown_hours: dto.regrab_cooldown_hours ?? WATCHDOG_DEFAULTS.qbit_regrab_cooldown_hours,
    qbit_max_consecutive_no_better: dto.max_consecutive_no_better ?? WATCHDOG_DEFAULTS.qbit_max_consecutive_no_better,
    qbit_custom_unregistered_msgs: [...(dto.custom_unregistered_msgs ?? [])],
    qbit_enabled: Boolean(dto.enabled),
  };
}

export interface InstanceFormDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (v: boolean) => void;
  readonly mode: 'create' | 'edit';
  readonly initial?: Partial<FormValues> | undefined;
}

function coerceEnum<T extends string>(
  value: string | null | undefined,
  allowed: readonly T[],
  fallback: T,
): T {
  return (allowed as readonly string[]).includes(value ?? '') ? (value as T) : fallback;
}

function formFromDetail(d: InstanceDetail): Omit<FormValues, keyof typeof WATCHDOG_DEFAULTS> {
  return {
    ...FORM_DEFAULTS,
    name: d.name ?? '',
    url: d.url ?? FORM_DEFAULTS.url,
    public_url: d.public_url ?? '',
    webhook_install_enabled: d.webhook_install_enabled ?? true,
    webhook_url_override: d.webhook_url_override ?? '',
    api_key: '',
    mode: coerceEnum(d.mode, ['auto', 'manual'] as const, FORM_DEFAULTS.mode),
    dry_run: dryRunFromWire(d.dry_run),
    timeout_sec: d.timeout_sec ?? FORM_DEFAULTS.timeout_sec,
    search_timeout_sec: d.search_timeout_sec ?? FORM_DEFAULTS.search_timeout_sec,
    tags_mode: coerceEnum(d.tags?.mode, ['off', 'include', 'exclude', 'both'] as const, FORM_DEFAULTS.tags_mode),
    tags_include: [...(d.tags?.include ?? [])],
    tags_exclude: [...(d.tags?.exclude ?? [])],
    search_require_all_aired: Boolean(d.search?.require_all_aired),
    search_skip_specials: Boolean(d.search?.skip_specials),
    search_skip_anime: Boolean(d.search?.skip_anime),
    search_min_custom_format_score: d.search?.min_custom_format_score ?? 0,
    ranking_indexer_priority_enabled: Boolean(d.ranking?.indexer_priority_enabled),
    ranking_origin_bonus: d.ranking?.origin_bonus ?? 0,
    rate_limit_rpm: d.rate_limit_rpm ?? 0,
    rate_limit_burst: d.rate_limit_burst ?? 0,
    limits_scan_max_series: d.limits?.scan_max_series ?? 0,
    limits_max_grabs_per_scan: d.limits?.max_grabs_per_scan ?? FORM_DEFAULTS.limits_max_grabs_per_scan,
    cooldown_mode: coerceEnum(d.cooldown?.mode, ['smart', 'strict'] as const, FORM_DEFAULTS.cooldown_mode),
    cooldown_series_after_grab_sec: d.cooldown?.series_after_grab_sec ?? FORM_DEFAULTS.cooldown_series_after_grab_sec,
    cooldown_guid_after_failed_grab_sec: d.cooldown?.guid_after_failed_grab_sec ?? FORM_DEFAULTS.cooldown_guid_after_failed_grab_sec,
    cooldown_guid_after_failed_import_sec: d.cooldown?.guid_after_failed_import_sec ?? FORM_DEFAULTS.cooldown_guid_after_failed_import_sec,
    retry_max_attempts: d.retry?.max_attempts ?? FORM_DEFAULTS.retry_max_attempts,
    retry_initial_backoff_sec: d.retry?.initial_backoff_sec ?? FORM_DEFAULTS.retry_initial_backoff_sec,
    retry_max_backoff_sec: d.retry?.max_backoff_sec ?? FORM_DEFAULTS.retry_max_backoff_sec,
    health_recheck_auth_sec: d.health_check?.recheck_auth_sec ?? FORM_DEFAULTS.health_recheck_auth_sec,
    health_recheck_network_sec: d.health_check?.recheck_network_sec ?? FORM_DEFAULTS.health_recheck_network_sec,
  };
}

function valuesToPayload(v: FormValues): Omit<InstanceCreateRequest, 'api_key'> {
  const dr = dryRunToWire(v.dry_run as DryRunChoice);
  const base: Omit<InstanceCreateRequest, 'api_key'> = {
    name: v.name,
    url: v.url,
    mode: v.mode,
    timeout_sec: v.timeout_sec,
    search_timeout_sec: v.search_timeout_sec,
    webhook_install_enabled: v.webhook_install_enabled,
    tags: {
      mode: v.tags_mode as DtoInstanceTagsMode,
      include: [...v.tags_include],
      exclude: [...v.tags_exclude],
    },
    search: {
      require_all_aired: v.search_require_all_aired,
      skip_specials: v.search_skip_specials,
      skip_anime: v.search_skip_anime,
      min_custom_format_score: v.search_min_custom_format_score,
    },
    ranking: {
      indexer_priority_enabled: v.ranking_indexer_priority_enabled,
      origin_bonus: v.ranking_origin_bonus,
    },
    limits: {
      scan_max_series: v.limits_scan_max_series,
      max_grabs_per_scan: v.limits_max_grabs_per_scan,
    },
    rate_limit_rpm: v.rate_limit_rpm,
    rate_limit_burst: v.rate_limit_burst,
    cooldown: {
      mode: v.cooldown_mode as DtoInstanceCooldownMode,
      series_after_grab_sec: v.cooldown_series_after_grab_sec,
      guid_after_failed_grab_sec: v.cooldown_guid_after_failed_grab_sec,
      guid_after_failed_import_sec: v.cooldown_guid_after_failed_import_sec,
    },
    retry: {
      max_attempts: v.retry_max_attempts,
      initial_backoff_sec: v.retry_initial_backoff_sec,
      max_backoff_sec: v.retry_max_backoff_sec,
    },
    health_check: {
      recheck_auth_sec: v.health_recheck_auth_sec,
      recheck_network_sec: v.health_recheck_network_sec,
    },
  };
  let out: Omit<InstanceCreateRequest, 'api_key'> = base;
  if (dr !== undefined) out = { ...out, dry_run: dr };
  const pu = v.public_url.trim();
  if (pu !== '') out = { ...out, public_url: pu };
  const wo = v.webhook_url_override.trim();
  if (wo !== '') out = { ...out, webhook_url_override: wo };
  return out;
}

export function InstanceFormDialog({
  open, onOpenChange, mode, initial,
}: InstanceFormDialogProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  const probe = useTestInstance();
  const save = useSaveInstanceWithQbit();
  const [probeResult, setProbeResult] = useState<string | null>(null);
  // Accordion open keys — local state so background refetches cannot
  // collapse the user's section. Default = connection open only.
  const [openSections, setOpenSections] = useState<string[]>(['connection']);
  // True between the first open-transition and the next close. Gates
  // the "seed openSections" branch of the re-seed effect so that an
  // already-expanded section (e.g. Tuning) never collapses just because
  // RHF's isDirty briefly flips back to false — this happens whenever
  // the user toggles a segmented control (e.g. cooldown_mode
  // smart↔strict) and the form value transiently re-matches the
  // registered default. See finding N-3 in the Phase 6 audit.
  const openedRef = useRef<boolean>(false);

  const detailQuery = useInstanceDetail(isEdit ? (initial?.name ?? null) : null);
  const detail = detailQuery.data?.detail;

  const qbitQuery = useQbitSettings(isEdit ? (initial?.name ?? null) : null);
  const qbitDTO = qbitQuery.data ?? null;

  const {
    register, handleSubmit, reset, getValues, setFocus, setValue, watch, control,
    formState: { errors, isSubmitting, isDirty, dirtyFields },
  } = useForm<FormValues>({
    resolver: zodResolver(pickSchema(mode)),
    defaultValues: { ...FORM_DEFAULTS, ...WATCHDOG_DEFAULTS, ...initial, api_key: '' },
    mode: 'onBlur',
  });

  const webhookInstallEnabled = useWatch({
    control,
    name: 'webhook_install_enabled',
    defaultValue: true,
  });

  // Reset the open-transition gate whenever the dialog closes. Next
  // open-transition will be allowed to seed openSections exactly once.
  useEffect(() => {
    if (!open) openedRef.current = false;
  }, [open]);

  // Section-seed: runs ONLY on the open-transition. Decoupled from
  // `isDirty` and `detail` so a late detail arrival cannot re-collapse
  // the Tuning section (Story 065 N-3 invariant), AND a deep-link open
  // with no detail yet still seeds the connection panel exactly once.
  useEffect(() => {
    if (!open) return;
    if (openedRef.current) return;
    openedRef.current = true;
    setOpenSections(['connection']);
  }, [open]);

  // Form-populate: fires whenever the resolved detail/qbit/initial
  // payload changes. Independent of openedRef so the late arrival of
  // `detail` after a `?edit=<name>` deep-link still seeds the form
  // (Story 074). `isDirty` guard preserves the "don't clobber
  // in-progress edits" invariant from Story 057.
  useEffect(() => {
    if (!open || isDirty) return;
    if (isEdit) {
      if (detail) {
        reset({ ...formFromDetail(detail), ...qbitFromDTO(qbitDTO) });
        // reason: this is the form-seed effect coordinating three async
        // sources (open transition + GET detail + GET qbit DTO). Folding
        // `setProbeResult(null)` into a derive-state-during-render pattern
        // would require tracking a 4-way prev-state tuple; not worth the
        // complexity for a side-state clear. See 057a4 / 060 for context.
        // eslint-disable-next-line react-hooks/set-state-in-effect
        setProbeResult(null);
      }
    } else {
      reset({ ...FORM_DEFAULTS, ...WATCHDOG_DEFAULTS, ...initial, api_key: '' });
      setProbeResult(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, isEdit, initial?.name, detail, qbitDTO, isDirty, reset]);

  const onInvalid = (errs: FieldErrors<FormValues>) => {
    if (!isEdit && errs.api_key) {
      setOpenSections((cur) => Array.from(new Set([...cur, 'connection'])));
      setFocus('api_key');
      toast.error(t('settings.instances.form.saveFailedConnectionSection'));
      return;
    }
    const has = (...names: (keyof FormValues)[]) => names.some((n) => errs[n]);
    let section: 'connection' | 'tuning';
    if (has(
      'name', 'url', 'api_key', 'mode', 'dry_run',
      'public_url', 'webhook_url_override', 'webhook_install_enabled',
    )) {
      section = 'connection';
    } else {
      section = 'tuning';
    }
    setOpenSections((cur) => Array.from(new Set([...cur, section])));
    const sectionLabel = t(`settings.instances.form.sections.${section}`);
    toast.error(t('settings.instances.form.saveFailedSectionFmt', { section: sectionLabel }));
  };

  const onSubmit = handleSubmit(async (values) => {
    const wire = valuesToPayload(values);

    // qBit dirty-bit detection: any qbit_ field dirty → send qbit body.
    const qbitDirty = Object.keys(dirtyFields).some(
      (k) => k.startsWith('qbit_'),
    );
    const qbitBody: QbitSettingsUpsertRequest | undefined = qbitDirty
      ? {
          url: (values.qbit_url ?? '').trim(),
          qbit_public_url: (values.qbit_public_url ?? '').trim(),
          username: (values.qbit_username ?? '').trim(),
          // Empty password = keep ciphertext (039d AC-4 invariant).
          password: values.qbit_password ?? '',
          category: (values.qbit_category ?? '').trim(),
          poll_interval_minutes: values.qbit_poll_interval_minutes,
          regrab_cooldown_hours: values.qbit_regrab_cooldown_hours,
          max_consecutive_no_better: values.qbit_max_consecutive_no_better,
          custom_unregistered_msgs: [...(values.qbit_custom_unregistered_msgs ?? [])],
          enabled: Boolean(values.qbit_enabled),
        }
      : undefined;

    let instanceBody: InstanceCreateRequest | InstanceUpdateRequest;
    if (isEdit && initial?.name) {
      if (!detail) return;
      const userTypedKey = Boolean(dirtyFields.api_key) && values.api_key.trim().length > 0;
      instanceBody = {
        ...wire,
        ...(userTypedKey ? { api_key: values.api_key.trim() } : {}),
      } as InstanceUpdateRequest;
    } else {
      instanceBody = { ...wire, api_key: values.api_key.trim() } as InstanceCreateRequest;
    }

    const result = await save.mutateAsync({
      mode,
      name: initial?.name,
      instanceBody,
      qbitBody,
    });

    // Re-seed the form to the persisted values so isDirty clears.
    reset({
      ...formFromDetail(result.detail),
      ...qbitFromDTO(qbitDTO),
      // Critical: clear the password input post-save (dirty-bit).
      qbit_password: '',
    });

    if (result.qbitError) {
      // Partial success: instance saved, qBit not. Leave dialog open
      // so the operator can retry without losing the qBit fields.
      toast.error(
        t('settings.instances.form.watchdog.actions.partialSuccessToast', {
          error: result.qbitError.message,
        }),
      );
      return;
    }

    onOpenChange(false);
  }, onInvalid);

  const onTest = async () => {
    setProbeResult(null);
    const { url, api_key } = getValues();
    if (!url || !api_key) {
      setProbeResult(t('settings.instances.form.probeNeedsCredentials'));
      return;
    }
    try {
      const resp = await probe.mutateAsync({ url, api_key });
      if (resp.ok) {
        setProbeResult(resp.version && resp.version.length > 0
          ? t('settings.instances.form.probeConnected', { version: resp.version })
          : t('settings.instances.form.probeConnectedUnknownVersion'));
      } else {
        setProbeResult(resp.reason || t('settings.instances.form.probeConnectionFailed'));
      }
    } catch {
      // network failure: leave result as null
    }
  };

  const editBlocked = isEdit && (detailQuery.isPending || detailQuery.isError || !detail);
  const uiUrlHint = useMemo(() => (isEdit ? detail?.ui_url : undefined), [isEdit, detail]);
  const tValErr = (msg: string | undefined) => tValidationError(msg, t);

  const title = isEdit
    ? t('settings.instances.form.editTitle')
    : t('settings.instances.form.createTitle');
  // Subtitle MUST NOT fall back to createSub while in edit mode — even
  // if `detail` is still loading. See Story 074 root cause: the
  // operator deep-linking via `?edit=<name>` would otherwise read
  // "новый Sonarr-сервер" on top of a half-populated form.
  let subtitle: string;
  if (isEdit && initial?.name) {
    subtitle = detail
      ? t('settings.instances.form.header.editSub', {
          name: initial.name,
          url: detail.url ?? '',
        })
      : t('settings.instances.form.header.editSubLoading', { name: initial.name });
  } else {
    subtitle = t('settings.instances.form.header.createSub');
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[640px] p-0 grid grid-rows-[auto_1fr_auto] max-h-[calc(100vh-80px)]"
      >
        <DialogHeader className="px-5 py-4 border-b border-border-faint">
          <DialogTitle className="text-[16px] font-[650] tracking-[-0.01em]">
            {title}
          </DialogTitle>
          <DialogDescription className="font-mono text-[12px] text-tx-faint">
            {subtitle}
          </DialogDescription>
        </DialogHeader>

        <form
          onSubmit={onSubmit}
          className="flex flex-col gap-4 overflow-y-auto overflow-x-hidden px-5 py-4 min-h-0"
          noValidate
        >
          {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
          <PromotedControls control={control as unknown as any} />

          <Accordion
            type="multiple"
            value={openSections}
            onValueChange={(v) => setOpenSections(v as string[])}
            className="flex flex-col gap-3"
          >
            <AccordionSection
              value="connection"
              icon={<Cable className="w-[15px] h-[15px]" />}
              title={t('settings.instances.form.sections.connection')}
            >
              <ConnectionSection
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                control={control as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                register={register as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                errors={errors as unknown as any}
                mode={mode}
                instanceName={initial?.name ?? undefined}
                installEnabled={Boolean(webhookInstallEnabled)}
                uiUrlHint={uiUrlHint}
                onTest={onTest}
                testing={probe.isPending}
                probeResult={probeResult}
                tValidationError={tValErr}
              />
            </AccordionSection>

            <AccordionSection
              value="tuning"
              icon={<SlidersHorizontal className="w-[15px] h-[15px]" />}
              title={t('settings.instances.form.sections.tuning')}
              subLabel={t('settings.instances.form.sections.tuningSubLabel')}
            >
              <TuningSection
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                control={control as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                register={register as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                errors={errors as unknown as any}
                tValidationError={tValErr}
              />
            </AccordionSection>

            <AccordionSection
              value="watchdog"
              icon={<ShieldCheck className="w-[15px] h-[15px]" />}
              title={t('settings.instances.form.sections.watchdog')}
              alwaysPill={t('settings.instances.form.sections.alwaysPill')}
            >
              <WatchdogSection
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                control={control as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                register={register as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                errors={errors as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                setValue={setValue as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                getValues={getValues as unknown as any}
                /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
                watch={watch as unknown as any}
                mode={mode}
                instanceName={initial?.name ?? undefined}
                tValidationError={tValErr}
              />
            </AccordionSection>
          </Accordion>
        </form>

        <DirtyFooter
          mode={mode}
          isDirty={isDirty}
          isSubmitting={isSubmitting}
          editBlocked={editBlocked}
          onCancel={() => onOpenChange(false)}
          onSubmit={() => { void onSubmit(); }}
        />
      </DialogContent>
    </Dialog>
  );
}
