import { useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Controller, type Control, type FieldPath, type FieldValues } from 'react-hook-form';
import { X, Plus } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';

export type DryRunChoice = 'auto' | 'on' | 'off';

export function dryRunFromWire(v: boolean | undefined | null): DryRunChoice {
  if (v === true) return 'on';
  if (v === false) return 'off';
  return 'auto';
}

export function dryRunToWire(c: DryRunChoice): boolean | undefined {
  if (c === 'on') return true;
  if (c === 'off') return false;
  return undefined;
}

export interface NumberFieldProps<T extends FieldValues> {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<T, any, any>;
  readonly name: FieldPath<T>;
  readonly label: string;
  readonly id: string;
  readonly min?: number;
  readonly max?: number;
  readonly step?: number;
  readonly suffix?: string;
  readonly hint?: string;
  readonly error?: string | undefined;
}

export function NumberField<T extends FieldValues>({
  control, name, label, id, min, max, step, suffix, hint, error,
}: NumberFieldProps<T>) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}{suffix ? <span className="text-muted ml-1">({suffix})</span> : null}</Label>
      <Controller
        control={control}
        name={name}
        render={({ field }) => (
          <Input
            id={id}
            type="number"
            inputMode="numeric"
            min={min}
            max={max}
            step={step ?? 1}
            value={field.value as number | string}
            onChange={(e) => field.onChange(e.target.value)}
            onBlur={() => {
              const raw = field.value;
              if (raw === '' || raw === '-' || raw === null || raw === undefined) {
                field.onChange(0);
              } else {
                const n = Number(raw);
                field.onChange(Number.isFinite(n) ? n : 0);
              }
              field.onBlur();
            }}
            aria-invalid={Boolean(error) || undefined}
          />
        )}
      />
      {hint && <p className="text-[11.5px] text-muted">{hint}</p>}
      {error && <p role="alert" className="text-status-danger text-[11.5px]">{error}</p>}
    </div>
  );
}

export interface SwitchFieldProps<T extends FieldValues> {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<T, any, any>;
  readonly name: FieldPath<T>;
  readonly label: string;
  readonly id: string;
  readonly hint?: string;
}

export function SwitchField<T extends FieldValues>({
  control, name, label, id, hint,
}: SwitchFieldProps<T>) {
  return (
    <div className="flex items-start gap-3">
      <Controller
        control={control}
        name={name}
        render={({ field }) => (
          <Switch
            id={id}
            checked={Boolean(field.value)}
            onCheckedChange={(v) => field.onChange(v)}
          />
        )}
      />
      <div className="flex flex-col gap-0.5">
        <Label htmlFor={id} className="font-normal">{label}</Label>
        {hint && <p className="text-[11.5px] text-muted">{hint}</p>}
      </div>
    </div>
  );
}

export interface TagListEditorProps {
  readonly value: readonly string[];
  readonly onChange: (next: readonly string[]) => void;
  readonly id: string;
  readonly placeholder?: string;
}

export function TagListEditor({ value, onChange, id, placeholder }: TagListEditorProps) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);

  const commit = () => {
    const pieces = draft
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
    if (pieces.length === 0) {
      setError(null);
      return;
    }
    for (const p of pieces) {
      if (p.length > 64) { setError(t('settings.instances.tagsEditor.tooLong', { tag: p })); return; }
    }
    const merged = [...value];
    for (const p of pieces) {
      if (!merged.includes(p)) merged.push(p);
    }
    onChange(merged);
    setDraft('');
    setError(null);
  };

  const remove = (idx: number) => {
    onChange(value.slice(0, idx).concat(value.slice(idx + 1)));
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      commit();
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-1.5 min-h-6">
        {value.length === 0 && (
          <span className="text-[11.5px] text-muted">{t('settings.instances.tagsEditor.empty')}</span>
        )}
        {value.map((tag, i) => (
          <Badge key={`${tag}-${i}`} variant="secondary" className="gap-1 pl-2.5 pr-1">
            {tag}
            <button
              type="button"
              aria-label={t('settings.instances.tagsEditor.removeAria', { tag })}
              onClick={() => remove(i)}
              className="rounded hover:bg-surface-2 p-0.5"
            >
              <X className="w-3 h-3" />
            </button>
          </Badge>
        ))}
      </div>
      <div className="flex gap-2">
        <Input
          id={id}
          value={draft}
          placeholder={placeholder ?? t('settings.instances.tagsEditor.placeholder')}
          onChange={(e) => { setDraft(e.target.value); setError(null); }}
          onKeyDown={onKey}
          onBlur={commit}
          aria-invalid={Boolean(error) || undefined}
        />
        <Button type="button" variant="outline" onClick={commit} className="gap-1.5">
          <Plus className="w-3.5 h-3.5" /> {t('settings.instances.tagsEditor.addButton')}
        </Button>
      </div>
      {error && <p role="alert" className="text-status-danger text-[11.5px]">{error}</p>}
    </div>
  );
}

export const FORM_DEFAULTS = {
  name: '',
  url: 'http://sonarr:8989',
  api_key: '',
  mode: 'auto' as const,
  dry_run: 'auto' as DryRunChoice,
  timeout_sec: 10,
  search_timeout_sec: 60,
  tags_mode: 'off' as const,
  tags_include: [] as string[],
  tags_exclude: [] as string[],
  search_require_all_aired: false,
  search_skip_specials: false,
  search_skip_anime: false,
  search_min_custom_format_score: 0,
  ranking_indexer_priority_enabled: false,
  ranking_origin_bonus: 0,
  rate_limit_rpm: 0,
  rate_limit_burst: 0,
  limits_scan_max_series: 0,
  limits_max_grabs_per_scan: 10,
  cooldown_mode: 'smart' as const,
  cooldown_series_after_grab_sec: 86400,
  cooldown_guid_after_failed_grab_sec: 259200,
  cooldown_guid_after_failed_import_sec: 172800,
  retry_max_attempts: 3,
  retry_initial_backoff_sec: 1,
  retry_max_backoff_sec: 30,
  health_recheck_auth_sec: 300,
  health_recheck_network_sec: 60,
};
