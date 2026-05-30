import { useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Plus } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { isValidCIDR } from '@/lib/cidr';

export interface TrustedProxiesEditorProps {
  readonly value: readonly string[];
  readonly onChange: (next: readonly string[]) => void;
  readonly id?: string;
}

export function TrustedProxiesEditor({ value, onChange, id }: TrustedProxiesEditorProps) {
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
      setError(t('settings.security.proxies.invalidCidr', { cidr: v }));
      return;
    }
    if (value.includes(v)) {
      setError(t('settings.security.proxies.duplicate', { cidr: v }));
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
            {t('settings.security.proxies.emptyHint')}
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
              aria-label={t('settings.security.proxies.removeAria', { cidr: p })}
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
          placeholder={t('settings.security.proxies.addPlaceholder')}
          aria-invalid={Boolean(error) || undefined}
        />
        <Button type="button" variant="outline" onClick={add} className="gap-1.5">
          <Plus className="w-3.5 h-3.5" /> {t('settings.security.proxies.addButton')}
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-status-danger text-[11.5px]">{error}</p>
      )}
    </div>
  );
}
