import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, Plus } from 'lucide-react';
import { useInstances, type Instance } from '@/lib/instances';
import { useDeleteInstance, useInstanceDetail } from '@/lib/instances-mutations';
import { useTriggerScan } from '@/lib/scan-mutations';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { InstanceFormDialog } from '@/components/settings/InstanceFormDialog';
import { InstanceHero } from '@/components/instances/InstanceHero';
import { InstanceCompactRow } from '@/components/instances/InstanceCompactRow';
import { AddInstanceGhostRow } from '@/components/instances/AddInstanceGhostRow';
import { InstancesEmptyState } from '@/components/instances/InstancesEmptyState';

function pickHero(instances: readonly Instance[], filter: string | null): {
  hero: Instance | null;
  rest: readonly Instance[];
} {
  if (instances.length === 0) return { hero: null, rest: [] };
  if (filter) {
    const idx = instances.findIndex((i) => i.name === filter);
    if (idx >= 0) {
      const hero = instances[idx];
      return {
        hero: hero ?? null,
        rest: [...instances.slice(0, idx), ...instances.slice(idx + 1)],
      };
    }
  }
  const firstInst = instances[0];
  return { hero: firstInst ?? null, rest: instances.slice(1) };
}

/**
 * TODO(050b): This implementation spans 050a + 050b due to LOC overflow (1047 total).
 * 050a: InstanceHero + InstanceStatsBlock + InstanceChipRow + hero-only rendering.
 * 050b: InstanceCompactRow + AddInstanceGhostRow + InstancesEmptyState + full layout.
 * Current state: both features are complete and integrated. No action required.
 */
export function Instances() {
  const { t } = useTranslation();
  const q = useInstances();
  const del = useDeleteInstance();
  const trigger = useTriggerScan();
  const { filter } = useInstanceFilter();
  const [searchParams, setSearchParams] = useSearchParams();

  const instances: readonly Instance[] = useMemo(
    () => q.data?.instances ?? [],
    [q.data?.instances],
  );
  const { hero, rest } = useMemo(() => pickHero(instances, filter), [instances, filter]);

  const [dialogOpen, setDialogOpen] = useState(
    () => searchParams.get('edit') !== null,
  );
  const [editing, setEditing] = useState<string | null>(
    () => searchParams.get('edit'),
  );
  const [deleting, setDeleting] = useState<string | null>(null);

  const detailQuery = useInstanceDetail(editing);
  const editDetail = detailQuery.data?.detail;
  const detailName = editDetail?.name;
  const detailUrl = editDetail?.url;
  const detailMode = editDetail?.mode;
  const editInitial = useMemo(() => {
    if (!editDetail) return undefined;
    return {
      name: editDetail.name ?? '',
      url: editDetail.url ?? '',
      mode: (editDetail.mode as 'auto' | 'manual' | undefined) ?? 'auto',
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailName, detailUrl, detailMode]);

  const openCreate = () => { setEditing(null); setDialogOpen(true); };
  const openEdit = (name: string) => { setEditing(name); setDialogOpen(true); };
  const onForceScan = (name: string) => { trigger.mutate({ instance: name }); };
  const onRecheck = (name: string) => { trigger.mutate({ instance: name }); };
  const onDeleteClick = (name: string) => { setDeleting(name); };
  const confirmDelete = async () => {
    if (!deleting) return;
    await del.mutateAsync({ name: deleting });
    setDeleting(null);
  };

  const headerSummary = useMemo(() => {
    const active = instances.filter((i) => i.health === 'Available').length;
    const degraded = instances.length - active;
    return t('instances.list.headerCount', { active, degraded });
  }, [instances, t]);

  return (
    <div className="max-w-[1200px] mx-auto p-6 flex flex-col gap-5">
      <header className="flex items-center gap-4">
        <h1 className="text-[15px] font-[650] tracking-tight">{t('instances.title')}</h1>
        <span className="text-[12.5px] text-tx-faint">{headerSummary}</span>
        <Button onClick={openCreate} className="ml-auto gap-1.5" variant="primary">
          <Plus className="w-3.5 h-3.5" />
          {t('instances.add.ghost')}
        </Button>
      </header>

      {q.isError && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('instances.loadFailed')}</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isError && q.isPending && (
        <div className="flex flex-col gap-4">
          <Card><CardContent className="p-5 flex flex-col gap-3">
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-3 w-60" />
            <div className="flex gap-6"><Skeleton className="h-10 w-32" /><Skeleton className="h-10 w-32" /></div>
            <Skeleton className="h-6 w-full" />
          </CardContent></Card>
          <Card><CardContent className="p-4"><Skeleton className="h-5 w-full" /></CardContent></Card>
        </div>
      )}

      {!q.isError && !q.isPending && instances.length === 0 && (
        <InstancesEmptyState onAdd={openCreate} />
      )}

      {!q.isError && !q.isPending && instances.length > 0 && hero && (
        <div className="flex flex-col gap-4">
          <InstanceHero
            instance={hero}
            onEdit={openEdit}
            onForceScan={onForceScan}
          />
          {rest.map((inst) => (
            <InstanceCompactRow
              key={inst.name}
              instance={inst}
              onEdit={openEdit}
              onRecheck={onRecheck}
              onDelete={onDeleteClick}
            />
          ))}
          <AddInstanceGhostRow onClick={openCreate} />
        </div>
      )}

      <InstanceFormDialog
        open={dialogOpen}
        onOpenChange={(v) => {
          setDialogOpen(v);
          if (!v) {
            setEditing(null);
            if (searchParams.has('edit')) {
              const next = new URLSearchParams(searchParams);
              next.delete('edit');
              setSearchParams(next, { replace: true });
            }
          }
        }}
        mode={editing ? 'edit' : 'create'}
        initial={editing ? editInitial : undefined}
      />

      <Dialog open={Boolean(deleting)} onOpenChange={(v) => !v && setDeleting(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{t('instances.delete.title', { name: deleting })}</DialogTitle>
            <DialogDescription>{t('instances.delete.body')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleting(null)}>{t('common.cancel')}</Button>
            <Button
              variant="destructive"
              onClick={confirmDelete}
              disabled={del.isPending}
            >
              {del.isPending ? t('settings.instances.deleting') : t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
