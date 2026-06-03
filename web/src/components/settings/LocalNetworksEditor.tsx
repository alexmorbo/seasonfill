import { useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Plus, RotateCcw } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { isValidCIDR } from '@/lib/cidr';

// Mirrors the backend defaults from 036a/036c (RFC1918 + loopback +
// link-local + ULA). Kept in sync at PR-review time; the source of
// truth is the Go side. Frontend re-asserting them gives the user a
// one-click "restore defaults" without a server round-trip.
export const LOCAL_NETWORK_DEFAULTS = Object.freeze([
  '127.0.0.0/8',
  '::1/128',
  '10.0.0.0/8',
  '172.16.0.0/12',
  '192.168.0.0/16',
  '169.254.0.0/16',
  'fe80::/10',
  'fc00::/7',
] as const);

export interface LocalNetworksEditorProps {
  readonly value: readonly string[];
  readonly onChange: (next: readonly string[]) => void;
  readonly id?: string;
}

export function LocalNetworksEditor({ value, onChange, id }: LocalNetworksEditorProps) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);

  const add = () => {
    const v = draft.trim();
    if (v === '') {
      setError(null);
      return;
    }
    if (!isValidCIDR(v)) {
      setError(t('settings.security.localNetworks.invalidCidr', { cidr: v }));
      return;
    }
    if (value.includes(v)) {
      setError(t('settings.security.localNetworks.duplicate', { cidr: v }));
      return;
    }
    onChange([...value, v]);
    setDraft('');
    setError(null);
  };

  const remove = (idx: number) => {
    const next = value.slice(0, idx).concat(value.slice(idx + 1));
    onChange(next);
  };

  const resetDefaults = () => {
    onChange([...LOCAL_NETWORK_DEFAULTS]);
    setDraft('');
    setError(null);
  };

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      add();
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-1.5">
        {value.length === 0 && (
          <span className="text-[11.5px] text-muted">
            {t('settings.security.localNetworks.emptyHint')}
          </span>
        )}
        {value.map((p, i) => (
          <Badge
            key={`${p}-${i}`}
            variant="secondary"
            className="gap-1 pl-2.5 pr-1 font-mono text-[11px]"
          >
            {p}
            <button
              type="button"
              aria-label={t('settings.security.localNetworks.removeAria', { cidr: p })}
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
          onChange={(e) => { setDraft(e.target.value); setError(null); }}
          onKeyDown={onKey}
          placeholder={t('settings.security.localNetworks.addPlaceholder')}
          aria-invalid={Boolean(error) || undefined}
        />
        <Button type="button" variant="outline" onClick={add} className="gap-1.5">
          <Plus className="w-3.5 h-3.5" /> {t('settings.security.localNetworks.addButton')}
        </Button>
        <Button
          type="button" variant="ghost" onClick={resetDefaults} className="gap-1.5"
          title={t('settings.security.localNetworks.resetDefaults')}
        >
          <RotateCcw className="w-3.5 h-3.5" /> {t('settings.security.localNetworks.resetDefaults')}
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-[11.5px] text-status-danger">{error}</p>
      )}
    </div>
  );
}
