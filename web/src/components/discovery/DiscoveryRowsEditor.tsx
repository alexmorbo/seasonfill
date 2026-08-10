import { useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ArrowUpDown, ArrowUp, ArrowDown, Trash2, Plus, Save, RotateCcw, X } from 'lucide-react';
import { toBcp47 } from '@/lib/locale';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Input } from '@/components/ui/input';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import { useDiscoveryGenresList, useDiscoveryNetworksList } from '@/api/discovery';
import {
  reorderRows, useSaveDiscoveryRows, useResetDiscoveryRows,
  type DiscoveryRow, type DiscoveryRowInput,
} from '@/api/discoveryRows';

// AddType is the closed set of add-row types (all params parsed by the discover
// engine). genre/network use pickers; keyword/watch_provider use id inputs.
type AddType = 'genre' | 'network' | 'keyword' | 'watch_provider';

// DraftRow adds a stable local key for React + DnD (persisted rows may share
// row_type, and new rows have no id yet).
interface DraftRow extends DiscoveryRowInput {
  key: string;
}

let uidSeq = 0;
const uid = () => `dr-${uidSeq++}`;

function toDraft(rows: readonly DiscoveryRow[]): DraftRow[] {
  return rows.map((r) => ({
    key: uid(),
    row_type: r.row_type,
    source: r.source,
    media_type: r.media_type,
    params: { ...r.params },
    position: r.position,
    enabled: r.enabled,
    title: r.title,
  }));
}

export function DiscoveryRowsEditor({
  initial, onExit,
}: {
  initial: readonly DiscoveryRow[];
  onExit: () => void;
}) {
  const { t, i18n } = useTranslation();
  const lang = toBcp47(i18n.resolvedLanguage);
  const [draft, setDraft] = useState<DraftRow[]>(() => toDraft(initial));
  const dragIndex = useRef<number | null>(null);

  const save = useSaveDiscoveryRows();
  const reset = useResetDiscoveryRows();

  const move = (from: number, to: number) =>
    setDraft((cur) => reorderRows(cur, from, to));
  const toggle = (key: string) =>
    setDraft((cur) => cur.map((r) => (r.key === key ? { ...r, enabled: !r.enabled } : r)));
  const remove = (key: string) =>
    setDraft((cur) => cur.filter((r) => r.key !== key));
  const append = (row: DiscoveryRowInput) =>
    setDraft((cur) => [...cur, { ...row, key: uid() }]);

  const onDrop = (index: number) => {
    const from = dragIndex.current;
    dragIndex.current = null;
    if (from === null) return;
    move(from, index);
  };

  const onSave = () => {
    const payload: DiscoveryRowInput[] = draft.map((r, i) => ({
      row_type: r.row_type,
      source: r.source,
      media_type: r.media_type,
      params: r.params,
      position: i,
      enabled: r.enabled,
      title: r.title,
    }));
    save.mutate(payload, {
      onSuccess: () => {
        toast.success(t('discovery.edit.saved'));
        onExit();
      },
      onError: (e) =>
        toast.error(t('discovery.edit.saveFailed', { error: e.message })),
    });
  };

  const onReset = () => {
    if (!window.confirm(t('discovery.edit.resetConfirm'))) return;
    reset.mutate(undefined, {
      onSuccess: () => {
        toast.success(t('discovery.edit.resetDone'));
        onExit();
      },
      onError: (e) =>
        toast.error(t('discovery.edit.resetFailed', { error: e.message })),
    });
  };

  return (
    <div className="space-y-4" data-testid="discovery-rows-editor">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-tx-primary">{t('discovery.edit.title')}</h2>
          <p className="text-xs text-tx-muted">{t('discovery.edit.hint')}</p>
        </div>
        <Button variant="ghost" size="sm" onClick={onExit} data-testid="discovery-edit-done">
          <X /> {t('discovery.edit.done')}
        </Button>
      </div>

      <ul className="space-y-2">
        {draft.map((row, index) => (
          <li
            key={row.key}
            draggable
            onDragStart={() => { dragIndex.current = index; }}
            onDragOver={(e) => e.preventDefault()}
            onDrop={() => onDrop(index)}
            className={cn(
              'flex items-center gap-2 rounded-md border border-border-subtle bg-bg-surface px-3 py-2',
              !row.enabled && 'opacity-60',
            )}
            data-testid={`discovery-edit-row-${index}`}
            data-row-type={row.row_type}
          >
            <span className="cursor-grab text-tx-muted" title={t('discovery.edit.drag')} aria-hidden>
              <ArrowUpDown className="size-4" />
            </span>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm text-tx-primary">{row.title}</div>
              <div className="text-[11px] text-tx-muted">
                {t(`discovery.rowType.${row.row_type}` as const)}
              </div>
            </div>
            <Button
              variant="ghost" size="icon-btn"
              disabled={index === 0}
              onClick={() => move(index, index - 1)}
              title={t('discovery.edit.moveUp')}
              data-testid={`discovery-edit-up-${index}`}
            >
              <ArrowUp />
            </Button>
            <Button
              variant="ghost" size="icon-btn"
              disabled={index === draft.length - 1}
              onClick={() => move(index, index + 1)}
              title={t('discovery.edit.moveDown')}
              data-testid={`discovery-edit-down-${index}`}
            >
              <ArrowDown />
            </Button>
            <Switch
              checked={row.enabled}
              onCheckedChange={() => toggle(row.key)}
              aria-label={t('discovery.edit.enabled')}
              data-testid={`discovery-edit-toggle-${index}`}
            />
            <Button
              variant="ghost" size="icon-btn"
              onClick={() => remove(row.key)}
              title={t('discovery.edit.remove')}
              data-testid={`discovery-edit-remove-${index}`}
            >
              <Trash2 />
            </Button>
          </li>
        ))}
      </ul>

      <AddRowPicker lang={lang} onAdd={append} />

      <div className="flex items-center gap-2 border-t border-border-subtle pt-3">
        <Button
          variant="primary"
          onClick={onSave}
          disabled={save.isPending || draft.length === 0}
          data-testid="discovery-edit-save"
        >
          <Save /> {save.isPending ? t('discovery.edit.saving') : t('discovery.edit.save')}
        </Button>
        <Button
          variant="outline"
          onClick={onReset}
          disabled={reset.isPending}
          data-testid="discovery-edit-reset"
        >
          <RotateCcw /> {t('discovery.edit.reset')}
        </Button>
      </div>
    </div>
  );
}

// AddRowPicker builds one new row. genre/network resolve a title from the
// picker endpoints; keyword/watch_provider take a numeric TMDB id (+ region
// for watch_provider, which TMDB requires alongside with_watch_providers).
function AddRowPicker({
  lang, onAdd,
}: {
  lang: string | undefined;
  onAdd: (row: DiscoveryRowInput) => void;
}) {
  const { t } = useTranslation();
  const [type, setType] = useState<AddType>('genre');
  const [genreId, setGenreId] = useState('');
  const [networkId, setNetworkId] = useState('');
  const [numId, setNumId] = useState('');
  const [region, setRegion] = useState('RU');

  const genres = useDiscoveryGenresList(lang);
  const networks = useDiscoveryNetworksList(lang);

  const nameById = useMemo(() => {
    const g = new Map<number, string>();
    for (const it of genres.data?.items ?? []) g.set(it.id, it.name);
    const n = new Map<number, string>();
    for (const it of networks.data?.items ?? []) n.set(it.id, it.name);
    return { g, n };
  }, [genres.data, networks.data]);

  const reset = () => { setGenreId(''); setNetworkId(''); setNumId(''); };

  const buildRow = (): DiscoveryRowInput | null => {
    const base = { source: 'tmdb_discover', media_type: 'tv', position: 0, enabled: true } as const;
    if (type === 'genre') {
      const id = Number(genreId);
      if (!id) return null;
      return { ...base, row_type: 'genre', title: nameById.g.get(id) ?? `${t('discovery.rowType.genre')} ${id}`,
        params: { with_genres: String(id), sort_by: 'popularity.desc' } };
    }
    if (type === 'network') {
      const id = Number(networkId);
      if (!id) return null;
      return { ...base, row_type: 'network', title: nameById.n.get(id) ?? `${t('discovery.rowType.network')} ${id}`,
        params: { with_networks: String(id), sort_by: 'popularity.desc' } };
    }
    if (type === 'keyword') {
      const id = Number(numId);
      if (!id) return null;
      return { ...base, row_type: 'keyword', title: `${t('discovery.rowType.keyword')} ${id}`,
        params: { with_keywords: String(id), sort_by: 'popularity.desc' } };
    }
    // watch_provider
    const id = Number(numId);
    if (!id) return null;
    const params: Record<string, string> = { with_watch_providers: String(id), sort_by: 'popularity.desc' };
    if (region.trim()) params.watch_region = region.trim().toUpperCase();
    return { ...base, row_type: 'watch_provider', title: `${t('discovery.rowType.watch_provider')} ${id}`, params };
  };

  const canAdd = (() => {
    if (type === 'genre') return Number(genreId) > 0;
    if (type === 'network') return Number(networkId) > 0;
    return Number(numId) > 0;
  })();

  const onConfirm = () => {
    const row = buildRow();
    if (!row) return;
    onAdd(row);
    reset();
  };

  const types: AddType[] = ['genre', 'network', 'keyword', 'watch_provider'];

  return (
    <div
      className="flex flex-wrap items-end gap-2 rounded-md border border-dashed border-border-subtle p-3"
      data-testid="discovery-add-row"
    >
      <div className="flex flex-col gap-1">
        <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.type')}</span>
        <Select value={type} onValueChange={(v) => { setType(v as AddType); reset(); }}>
          <SelectTrigger className="w-40" data-testid="discovery-add-type">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {types.map((ty) => (
              <SelectItem key={ty} value={ty}>{t(`discovery.edit.add.type_${ty}` as const)}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {type === 'genre' && (
        <div className="flex flex-col gap-1">
          <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.type_genre')}</span>
          <Select value={genreId} onValueChange={setGenreId}>
            <SelectTrigger className="w-48" data-testid="discovery-add-genre">
              <SelectValue placeholder={t('discovery.edit.add.genre_placeholder')} />
            </SelectTrigger>
            <SelectContent>
              {(genres.data?.items ?? []).map((g) => (
                <SelectItem key={g.id} value={String(g.id)}>{g.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {type === 'network' && (
        <div className="flex flex-col gap-1">
          <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.type_network')}</span>
          <Select value={networkId} onValueChange={setNetworkId}>
            <SelectTrigger className="w-48" data-testid="discovery-add-network">
              <SelectValue placeholder={t('discovery.edit.add.network_placeholder')} />
            </SelectTrigger>
            <SelectContent>
              {(networks.data?.items ?? []).map((n) => (
                <SelectItem key={n.id} value={String(n.id)}>{n.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {type === 'keyword' && (
        <div className="flex flex-col gap-1">
          <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.keyword_id')}</span>
          <Input
            type="number" min={1} inputMode="numeric" value={numId}
            onChange={(e) => setNumId(e.target.value)}
            className="w-40" data-testid="discovery-add-keyword"
            placeholder="e.g. 210024"
          />
        </div>
      )}

      {type === 'watch_provider' && (
        <>
          <div className="flex flex-col gap-1">
            <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.provider_id')}</span>
            <Input
              type="number" min={1} inputMode="numeric" value={numId}
              onChange={(e) => setNumId(e.target.value)}
              className="w-32" data-testid="discovery-add-provider"
              placeholder="e.g. 8"
            />
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-[11px] text-tx-muted">{t('discovery.edit.add.region')}</span>
            <Input
              value={region} onChange={(e) => setRegion(e.target.value)}
              className="w-20" data-testid="discovery-add-region" maxLength={2}
            />
          </div>
        </>
      )}

      <Button
        variant="secondary" size="sm"
        disabled={!canAdd}
        onClick={onConfirm}
        data-testid="discovery-add-confirm"
      >
        <Plus /> {t('discovery.edit.add.button')}
      </Button>
    </div>
  );
}
