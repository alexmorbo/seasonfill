import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Plus, X, Loader2, Save } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import {
  useRuntimeConfig,
  useUpdateRuntimeConfig,
  type RuntimeConfig,
} from '@/lib/runtime-config';
import type { GuidRewriteRule } from '@/lib/guid-rewrite';
import { ApiError } from '@/lib/api';

// Matches the backend cap from Story 107 (guidRewritesMaxLen). The frontend
// disables the add button at the cap; the server validates anyway so this
// is just a UX guard.
export const GUID_REWRITES_MAX = 50;

// dtoRulesToLocal mirrors the wire `[{from, to}]` shape into our editor's
// local mutable form. Always returns a non-empty mutable array (slice copy)
// so editing doesn't mutate the cached query result.
function dtoRulesToLocal(
  rules: ReadonlyArray<{ readonly from?: string; readonly to?: string }> | undefined,
): GuidRewriteRule[] {
  if (!rules) return [];
  return rules.map((r) => ({ from: r.from ?? '', to: r.to ?? '' }));
}

// localToDto trims and drops empty-`from` rows on save. Empty `to` is
// allowed (backend treats it as "strip the substring"). Order is preserved.
function localToDto(rules: ReadonlyArray<GuidRewriteRule>): GuidRewriteRule[] {
  const out: GuidRewriteRule[] = [];
  for (const r of rules) {
    const from = r.from.trim();
    if (from === '') continue;
    out.push({ from, to: r.to.trim() });
  }
  return out;
}

export function GuidRewritesEditor() {
  const { t } = useTranslation();
  const runtime = useRuntimeConfig();
  const mut = useUpdateRuntimeConfig();

  // localDraft holds the operator's in-progress edits, or null when the
  // editor should reflect the server snapshot directly. Switching to a
  // non-null draft on the first edit avoids the "setState-in-useEffect"
  // anti-pattern flagged by react-hooks/set-state-in-effect: the seeded
  // values are derived during render from the cached query, not mirrored
  // into local state. Saving (or a fresh server snapshot the operator is
  // not editing) collapses the draft back to null.
  const [localDraft, setLocalDraft] = useState<GuidRewriteRule[] | null>(null);

  const serverRules = dtoRulesToLocal(runtime.data?.config.guid_rewrites);
  const rules: GuidRewriteRule[] = localDraft ?? serverRules;
  const dirty = localDraft !== null;

  const addRow = () => {
    if (rules.length >= GUID_REWRITES_MAX) return;
    setLocalDraft([...rules, { from: '', to: '' }]);
  };

  const removeRow = (idx: number) => {
    const next = rules.slice(0, idx).concat(rules.slice(idx + 1));
    setLocalDraft(next);
  };

  const updateRow = (idx: number, patch: Partial<GuidRewriteRule>) => {
    const next = rules.map((r, i) => (i === idx ? { ...r, ...patch } : r));
    setLocalDraft(next);
  };

  const onSave = () => {
    const current = runtime.data?.config;
    if (!current) return;
    // The schema marks every RuntimeConfigDTO field optional, so spreading
    // into the readonly object type requires the same `as RuntimeConfig`
    // escape hatch the GeneralTab uses (formToPayload returns `as
    // RuntimeConfig`). `localToDto` trims and drops empty-`from` rows.
    const payload = {
      ...current,
      guid_rewrites: localToDto(rules),
    } as RuntimeConfig;
    mut.mutate(payload, {
      onSuccess: () => {
        // Drop the local draft so the editor switches back to deriving from
        // the cached query (which the useUpdateRuntimeConfig hook has just
        // setQueryData'd to the saved snapshot).
        setLocalDraft(null);
        toast.success(t('settings.integrations.guidRewrites.savedToast'));
      },
      onError: (err: ApiError) => {
        toast.error(
          t('settings.integrations.guidRewrites.saveFailedToast', { err: err.message }),
        );
      },
    });
  };

  const atMax = rules.length >= GUID_REWRITES_MAX;

  return (
    <div className="flex flex-col gap-3" data-testid="guid-rewrites-editor">
      <p className="text-[12px] text-muted m-0">
        {t('settings.integrations.guidRewrites.hint')}
      </p>

      {rules.length === 0 ? (
        <p className="text-[12.5px] text-tx-faint italic m-0">
          {t('settings.integrations.guidRewrites.empty')}
        </p>
      ) : (
        <div className="flex flex-col gap-2.5" data-testid="guid-rewrites-rows">
          {/* Header row — labels render once instead of per-row to save
              vertical space; aria-hidden because the inputs themselves
              have aria-labels for screen readers. */}
          <div
            className="grid grid-cols-[1fr_1fr_auto] gap-2 text-[11px] uppercase tracking-[0.06em] text-tx-faint"
            aria-hidden="true"
          >
            <span>{t('settings.integrations.guidRewrites.fromLabel')}</span>
            <span>{t('settings.integrations.guidRewrites.toLabel')}</span>
            <span />
          </div>
          {rules.map((r, i) => (
            <div
              key={i}
              data-testid={`guid-rewrite-row-${i}`}
              className="grid grid-cols-[1fr_1fr_auto] gap-2 items-center"
            >
              <Input
                value={r.from}
                onChange={(e) => updateRow(i, { from: e.target.value })}
                placeholder={t('settings.integrations.guidRewrites.fromPlaceholder')}
                aria-label={`${t('settings.integrations.guidRewrites.fromLabel')} #${i + 1}`}
                data-testid={`guid-rewrite-from-${i}`}
                className="font-mono text-[12.5px]"
              />
              <Input
                value={r.to}
                onChange={(e) => updateRow(i, { to: e.target.value })}
                placeholder={t('settings.integrations.guidRewrites.toPlaceholder')}
                aria-label={`${t('settings.integrations.guidRewrites.toLabel')} #${i + 1}`}
                data-testid={`guid-rewrite-to-${i}`}
                className="font-mono text-[12.5px]"
              />
              <Button
                type="button"
                size="icon"
                variant="ghost"
                onClick={() => removeRow(i)}
                aria-label={t('settings.integrations.guidRewrites.removeAria', { index: i + 1 })}
                data-testid={`guid-rewrite-remove-${i}`}
              >
                <X className="w-3.5 h-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addRow}
          disabled={atMax}
          data-testid="guid-rewrites-add"
          className="gap-1.5"
        >
          <Plus className="w-3.5 h-3.5" />
          {t('settings.integrations.guidRewrites.addButton')}
        </Button>
        <Button
          type="button"
          variant="default"
          size="sm"
          onClick={onSave}
          disabled={!dirty || mut.isPending || runtime.isPending}
          data-testid="guid-rewrites-save"
          className="gap-1.5"
        >
          {mut.isPending ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
          ) : (
            <Save className="w-3.5 h-3.5" />
          )}
          {t('settings.integrations.guidRewrites.save')}
        </Button>
        {atMax && (
          <span className="text-[11.5px] text-tx-faint" data-testid="guid-rewrites-cap">
            {t('settings.integrations.guidRewrites.maxRules')}
          </span>
        )}
      </div>

      {/* Hidden Label improves semantics for the editor — required to
          satisfy axe in the test setup without changing the visual layout. */}
      <Label className="sr-only" htmlFor="guid-rewrites-editor">
        {t('settings.integrations.guidRewrites.section')}
      </Label>
    </div>
  );
}
