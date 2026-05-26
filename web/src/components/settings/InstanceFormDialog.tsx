import { useEffect, useState } from 'react';
import { Controller, useForm, type FieldErrors } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { KeyRound, Loader2 } from 'lucide-react';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import {
  Tooltip, TooltipContent, TooltipProvider, TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@/components/ui/tabs';
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group';
import {
  useCreateInstance,
  useInstanceDetail,
  useTestInstance,
  useUpdateInstance,
  type InstanceCreateRequest,
  type InstanceUpdateRequest,
  type InstanceDetail,
} from '@/lib/instances-mutations';
import { DtoInstanceCooldownMode, DtoInstanceTagsMode } from '@/api/schema';
import {
  FORM_DEFAULTS, NumberField, SwitchField, TagListEditor,
  dryRunFromWire, dryRunToWire, type DryRunChoice,
} from './instance-form-fields';

const nameRule = z
  .string()
  .min(1, 'Name is required')
  .max(128, 'Max 128 characters')
  .regex(/^[a-zA-Z0-9_-]+$/, 'Allowed: a-z, A-Z, 0-9, _ and -');

const urlRule = z
  .string()
  .min(1, 'URL is required')
  .url('Must be a valid URL')
  .refine((v) => v.startsWith('http://') || v.startsWith('https://'),
    'URL must start with http:// or https://');

const modeRule = z.enum(['auto', 'manual']);
const dryRunRule = z.enum(['auto', 'on', 'off']);
const tagsModeRule = z.enum(['off', 'include', 'exclude', 'both']);
const cooldownModeRule = z.enum(['smart', 'strict']);

// Bounds mirror application/instance/usecase.go (60-88). Keeping them
// inline here is deliberate — the constants don't ship to TS and the
// values rarely move; a typo would surface as a server 400 the test
// suite would catch.
const int = (min: number, max: number, label: string) =>
  z.coerce.number().int(`${label} must be an integer`).min(min, `${label} >= ${min}`).max(max, `${label} <= ${max}`);
const float = (min: number, max: number, label: string) =>
  z.coerce.number().min(min, `${label} >= ${min}`).max(max, `${label} <= ${max}`);

const baseShape = {
  name: nameRule,
  url: urlRule,
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
  cooldown_series_after_grab_sec: int(0, 604800, 'cooldown.series_after_grab'),
  cooldown_guid_after_failed_grab_sec: int(0, 604800, 'cooldown.guid_after_failed_grab'),
  cooldown_guid_after_failed_import_sec: int(0, 604800, 'cooldown.guid_after_failed_import'),
  retry_max_attempts: int(0, 10, 'retry.max_attempts'),
  retry_initial_backoff_sec: int(0, 3600, 'retry.initial_backoff'),
  retry_max_backoff_sec: int(0, 3600, 'retry.max_backoff'),
  health_recheck_auth_sec: int(10, 86400, 'health.recheck_auth'),
  health_recheck_network_sec: int(10, 86400, 'health.recheck_network'),
};

const createSchema = z.object({ ...baseShape, api_key: z.string().min(1, 'API key required for new instances') });
const editSchema   = z.object({ ...baseShape, api_key: z.string() });
type FormValues = z.infer<typeof createSchema>;
const pickSchema = (m: 'create' | 'edit') => (m === 'create' ? createSchema : editSchema);

export interface InstanceFormDialogProps {
  readonly open: boolean;
  readonly onOpenChange: (v: boolean) => void;
  readonly mode: 'create' | 'edit';
  readonly initial?: Partial<FormValues> | undefined;
}

// Hydrate a full FormValues from a server-side InstanceDetail. Falls
// back to FORM_DEFAULTS for any field the server omitted (e.g. a row
// written by an older schema). Crucially does NOT touch api_key — the
// server-side mask "***" must never seed the form input or it will be
// re-encrypted over the real secret on Save (032b).
function formFromDetail(d: InstanceDetail): FormValues {
  return {
    ...FORM_DEFAULTS,
    name: d.name ?? '',
    url: d.url ?? FORM_DEFAULTS.url,
    api_key: '',
    mode: (d.mode === 'manual' ? 'manual' : 'auto'),
    dry_run: dryRunFromWire(d.dry_run),
    timeout_sec: d.timeout_sec ?? FORM_DEFAULTS.timeout_sec,
    search_timeout_sec: d.search_timeout_sec ?? FORM_DEFAULTS.search_timeout_sec,
    tags_mode: (d.tags?.mode as FormValues['tags_mode']) ?? FORM_DEFAULTS.tags_mode,
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
    cooldown_mode: (d.cooldown?.mode === 'strict' ? 'strict' : 'smart'),
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

// Build the wire payload from FormValues. Centralises the dry_run
// omit-on-auto rule and the tag-mode -> tags-shape mapping. Returns
// an object missing `api_key`; the caller layers it on per the dirty-
// bit rule (032b).
function valuesToPayload(v: FormValues): Omit<InstanceCreateRequest, 'api_key'> {
  const dr = dryRunToWire(v.dry_run as DryRunChoice);
  const base: Omit<InstanceCreateRequest, 'api_key'> = {
    name: v.name,
    url: v.url,
    mode: v.mode,
    timeout_sec: v.timeout_sec,
    search_timeout_sec: v.search_timeout_sec,
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
  // 'auto' -> omit the field; explicit on/off -> include the boolean.
  if (dr === undefined) return base;
  return { ...base, dry_run: dr };
}

export function InstanceFormDialog({
  open, onOpenChange, mode, initial,
}: InstanceFormDialogProps) {
  const isEdit = mode === 'edit';
  const create = useCreateInstance();
  const update = useUpdateInstance();
  const probe = useTestInstance();
  const [probeResult, setProbeResult] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<string>('connection');

  // Same reasoning as before 033a — useInstanceDetail backs the edit
  // hydration. Disabled in create mode (name=null).
  const detailQuery = useInstanceDetail(isEdit ? (initial?.name ?? null) : null);
  const detail = detailQuery.data?.detail;

  const {
    register, handleSubmit, reset, getValues, setFocus, control,
    formState: { errors, isSubmitting, dirtyFields },
  } = useForm<FormValues>({
    resolver: zodResolver(pickSchema(mode)),
    defaultValues: { ...FORM_DEFAULTS, ...initial, api_key: '' },
    mode: 'onBlur',
  });

  // On open OR on edit-target switch, rebuild defaults. In edit mode we
  // wait until `detail` arrives before re-seeding, so user keystrokes
  // aren't clobbered by an async refetch (mirrors the 028b/032b fix
  // for the api_key field).
  useEffect(() => {
    if (!open) return;
    if (isEdit) {
      if (detail) {
        reset(formFromDetail(detail));
        setProbeResult(null);
        setActiveTab('connection');
      }
    } else {
      reset({ ...FORM_DEFAULTS, ...initial, api_key: '' });
      setProbeResult(null);
      setActiveTab('connection');
    }
    // We DELIBERATELY do not depend on `initial` identity — the parent
    // refetches the instances list every 5s and a fresh object literal
    // would otherwise blow away in-flight input. Key by name only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, isEdit, initial?.name, detail, reset]);

  const onInvalid = (errs: FieldErrors<FormValues>) => {
    if (!isEdit && errs.api_key) {
      setActiveTab('connection');
      setFocus('api_key');
      return;
    }
    // Jump to the first tab containing an error so the user sees the
    // inline message without hunting. Order matches the visual tab row.
    const has = (...names: (keyof FormValues)[]) => names.some((n) => errs[n]);
    if (has('name', 'url', 'api_key', 'timeout_sec', 'search_timeout_sec')) setActiveTab('connection');
    else if (has('search_min_custom_format_score', 'tags_include', 'tags_exclude')) setActiveTab('behavior');
    else if (has('rate_limit_rpm', 'rate_limit_burst', 'limits_scan_max_series', 'limits_max_grabs_per_scan', 'ranking_origin_bonus')) setActiveTab('performance');
    else setActiveTab('advanced');
  };

  const onSubmit = handleSubmit(async (values) => {
    const wire = valuesToPayload(values);
    if (isEdit && initial?.name) {
      if (!detail) return;
      // 032b invariant: only attach api_key when RHF says the user
      // actually touched the field AND there's non-empty content. The
      // masked "***" from GET must NEVER end up in the PUT body. The
      // dirty-bit pattern survives because RHF tracks `api_key` on
      // its own — none of the new fields share that key.
      const userTypedKey = Boolean(dirtyFields.api_key) && values.api_key.trim().length > 0;
      const body: InstanceUpdateRequest = {
        ...wire,
        ...(userTypedKey ? { api_key: values.api_key.trim() } : {}),
      };
      await update.mutateAsync({ name: initial.name, body });
    } else {
      const body: InstanceCreateRequest = {
        ...wire,
        api_key: values.api_key.trim(),
      };
      await create.mutateAsync({ body });
    }
    onOpenChange(false);
  }, onInvalid);

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
        setProbeResult(resp.version && resp.version.length > 0
          ? `Connected to Sonarr ${resp.version}`
          : 'Connected (version unknown)');
      } else {
        setProbeResult(resp.reason || 'Connection failed');
      }
    } catch {
      // network failure: leave result as null (already reset at top)
    }
  };

  const editBlocked = isEdit && (detailQuery.isPending || detailQuery.isError || !detail);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{isEdit ? 'Edit instance' : 'Add Sonarr instance'}</DialogTitle>
        </DialogHeader>
        <DialogDescription className="sr-only">
          {mode === 'create'
            ? 'Create a new Sonarr instance.'
            : `Edit the ${initial?.name ?? ''} Sonarr instance configuration.`}
        </DialogDescription>

        <form onSubmit={onSubmit} className="flex flex-col gap-4" noValidate>
          <Tabs value={activeTab} onValueChange={setActiveTab}>
            <TabsList>
              <TabsTrigger value="connection">Connection</TabsTrigger>
              <TabsTrigger value="behavior">Behavior</TabsTrigger>
              <TabsTrigger value="performance">Performance</TabsTrigger>
              <TabsTrigger value="advanced">Advanced</TabsTrigger>
            </TabsList>

            {/* CONNECTION -------------------------------------------- */}
            <TabsContent value="connection" className="mt-4 flex flex-col gap-4">
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
                  placeholder={isEdit ? 'Leave blank to keep existing key' : ''}
                  aria-invalid={Boolean(errors.api_key) || undefined}
                  {...register('api_key')}
                />
                {isEdit && (
                  <p className="text-[11.5px] text-muted">
                    Leave the field empty to keep the current key. The stored
                    key is never sent to the browser.
                  </p>
                )}
                {errors.api_key && (
                  <p role="alert" className="text-status-danger text-[11.5px]">
                    {errors.api_key.message}
                  </p>
                )}
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="inst-mode">Mode</Label>
                  <Controller
                    name="mode"
                    control={control}
                    render={({ field }) => (
                      <Select value={field.value} onValueChange={field.onChange}>
                        <SelectTrigger id="inst-mode">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="auto">auto</SelectItem>
                          <SelectItem value="manual">manual</SelectItem>
                        </SelectContent>
                      </Select>
                    )}
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label>Dry run</Label>
                  <Controller
                    name="dry_run"
                    control={control}
                    render={({ field }) => (
                      <RadioGroup
                        value={field.value}
                        onValueChange={field.onChange}
                        className="grid grid-cols-3 gap-1.5"
                      >
                        {(['auto', 'on', 'off'] as const).map((c) => (
                          <Label
                            key={c}
                            htmlFor={`dry-${c}`}
                            className="flex items-center gap-1.5 border border-border rounded-md px-2 py-1.5 cursor-pointer hover:bg-surface-2 font-normal"
                          >
                            <RadioGroupItem id={`dry-${c}`} value={c} />
                            <span className="text-[12.5px]">{c}</span>
                          </Label>
                        ))}
                      </RadioGroup>
                    )}
                  />
                  <p className="text-[11.5px] text-muted">
                    auto = inherit global default; on/off override per-instance.
                  </p>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <NumberField
                  control={control}
                  name="timeout_sec"
                  id="inst-timeout"
                  label="Timeout"
                  suffix="seconds"
                  min={1} max={300}
                  error={errors.timeout_sec?.message}
                />
                <NumberField
                  control={control}
                  name="search_timeout_sec"
                  id="inst-search-timeout"
                  label="Search timeout"
                  suffix="seconds"
                  min={1} max={600}
                  error={errors.search_timeout_sec?.message}
                />
              </div>

              <div className="flex items-center gap-3 pt-1">
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
            </TabsContent>

            {/* BEHAVIOR ---------------------------------------------- */}
            <TabsContent value="behavior" className="mt-4 flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="inst-tags-mode">Tag mode</Label>
                <Controller
                  name="tags_mode"
                  control={control}
                  render={({ field }) => (
                    <Select value={field.value} onValueChange={field.onChange}>
                      <SelectTrigger id="inst-tags-mode">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="off">off</SelectItem>
                        <SelectItem value="include">include</SelectItem>
                        <SelectItem value="exclude">exclude</SelectItem>
                        <SelectItem value="both">both</SelectItem>
                      </SelectContent>
                    </Select>
                  )}
                />
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="inst-tags-include">Include tags</Label>
                  <Controller
                    name="tags_include"
                    control={control}
                    render={({ field }) => (
                      <TagListEditor
                        id="inst-tags-include"
                        value={field.value}
                        onChange={(next) => field.onChange([...next])}
                      />
                    )}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="inst-tags-exclude">Exclude tags</Label>
                  <Controller
                    name="tags_exclude"
                    control={control}
                    render={({ field }) => (
                      <TagListEditor
                        id="inst-tags-exclude"
                        value={field.value}
                        onChange={(next) => field.onChange([...next])}
                      />
                    )}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-y-2 gap-x-4 pt-1">
                <SwitchField control={control} name="search_require_all_aired"
                  id="search-require-all-aired" label="Require all aired"
                  hint="Only grab when all aired episodes are missing." />
                <SwitchField control={control} name="search_skip_specials"
                  id="search-skip-specials" label="Skip specials"
                  hint="Ignore season 0." />
                <SwitchField control={control} name="search_skip_anime"
                  id="search-skip-anime" label="Skip anime"
                  hint="Ignore series flagged as anime." />
                <NumberField control={control} name="search_min_custom_format_score"
                  id="search-mcfs" label="Min custom format score"
                  min={-1000} max={1000}
                  error={errors.search_min_custom_format_score?.message} />
              </div>
            </TabsContent>

            {/* PERFORMANCE ------------------------------------------- */}
            <TabsContent value="performance" className="mt-4 flex flex-col gap-4">
              <div className="grid grid-cols-2 gap-4">
                <NumberField control={control} name="rate_limit_rpm"
                  id="rate-limit-rpm" label="Rate limit" suffix="req/min"
                  min={0} max={10000}
                  error={errors.rate_limit_rpm?.message} />
                <NumberField control={control} name="rate_limit_burst"
                  id="rate-limit-burst" label="Rate limit burst"
                  min={0} max={10000}
                  error={errors.rate_limit_burst?.message} />
                <NumberField control={control} name="limits_scan_max_series"
                  id="limits-scan-max" label="Max series per scan"
                  min={0} max={100000}
                  hint="0 = no cap."
                  error={errors.limits_scan_max_series?.message} />
                <NumberField control={control} name="limits_max_grabs_per_scan"
                  id="limits-grabs" label="Max grabs per scan"
                  min={0} max={100}
                  error={errors.limits_max_grabs_per_scan?.message} />
                <NumberField control={control} name="ranking_origin_bonus"
                  id="ranking-origin-bonus" label="Origin bonus"
                  min={-100} max={100} step={0.1}
                  error={errors.ranking_origin_bonus?.message} />
                <SwitchField control={control} name="ranking_indexer_priority_enabled"
                  id="ranking-indexer-priority" label="Use indexer priority"
                  hint="Tiebreak by indexer priority field." />
              </div>
            </TabsContent>

            {/* ADVANCED ---------------------------------------------- */}
            <TabsContent value="advanced" className="mt-4 flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="cooldown-mode">Cooldown mode</Label>
                <Controller
                  name="cooldown_mode"
                  control={control}
                  render={({ field }) => (
                    <Select value={field.value} onValueChange={field.onChange}>
                      <SelectTrigger id="cooldown-mode">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="smart">smart</SelectItem>
                        <SelectItem value="strict">strict</SelectItem>
                      </SelectContent>
                    </Select>
                  )}
                />
              </div>
              <div className="grid grid-cols-3 gap-4">
                <NumberField control={control} name="cooldown_series_after_grab_sec"
                  id="cd-series" label="Series after grab" suffix="seconds"
                  min={0} max={604800}
                  error={errors.cooldown_series_after_grab_sec?.message} />
                <NumberField control={control} name="cooldown_guid_after_failed_grab_sec"
                  id="cd-guid-grab" label="GUID after failed grab" suffix="seconds"
                  min={0} max={604800}
                  error={errors.cooldown_guid_after_failed_grab_sec?.message} />
                <NumberField control={control} name="cooldown_guid_after_failed_import_sec"
                  id="cd-guid-import" label="GUID after failed import" suffix="seconds"
                  min={0} max={604800}
                  error={errors.cooldown_guid_after_failed_import_sec?.message} />
              </div>
              <div className="grid grid-cols-3 gap-4">
                <NumberField control={control} name="retry_max_attempts"
                  id="retry-attempts" label="Max retry attempts"
                  min={0} max={10}
                  error={errors.retry_max_attempts?.message} />
                <NumberField control={control} name="retry_initial_backoff_sec"
                  id="retry-initial" label="Initial backoff" suffix="seconds"
                  min={0} max={3600}
                  error={errors.retry_initial_backoff_sec?.message} />
                <NumberField control={control} name="retry_max_backoff_sec"
                  id="retry-max" label="Max backoff" suffix="seconds"
                  min={0} max={3600}
                  error={errors.retry_max_backoff_sec?.message} />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <NumberField control={control} name="health_recheck_auth_sec"
                  id="hc-auth" label="Recheck auth" suffix="seconds"
                  min={10} max={86400}
                  error={errors.health_recheck_auth_sec?.message} />
                <NumberField control={control} name="health_recheck_network_sec"
                  id="hc-net" label="Recheck network" suffix="seconds"
                  min={10} max={86400}
                  error={errors.health_recheck_network_sec?.message} />
              </div>
            </TabsContent>
          </Tabs>

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
