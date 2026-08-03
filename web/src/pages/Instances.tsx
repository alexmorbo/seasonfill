import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle, Plus } from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useInstances, type Instance } from '@/lib/instances';
import { useDeleteInstance, useInstanceDetail } from '@/lib/instances-mutations';
import { useTriggerScan } from '@/lib/scan-mutations';
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
import { AddInstanceGhostRow } from '@/components/instances/AddInstanceGhostRow';
import { InstancesEmptyState } from '@/components/instances/InstancesEmptyState';

export function Instances() {
  const { t } = useTranslation();
  useSetPageTitle(t('instances.title'));
  const q = useInstances();
  const del = useDeleteInstance();
  const trigger = useTriggerScan();
  const [searchParams, setSearchParams] = useSearchParams();

  const instances: readonly Instance[] = useMemo(
    () => q.data?.instances ?? [],
    [q.data?.instances],
  );

  // Story 494 (B-13): `?add=1` deep-link from Dashboard CTA opens
  // InstanceFormDialog in create mode on initial render. The legacy
  // `?edit=<name>` deep-link still opens it in edit mode. The query
  // params are stripped via `onOpenChange` below when the dialog closes
  // — same pattern as `?edit`, no setState-in-effect.
  const [dialogOpen, setDialogOpen] = useState(
    () => searchParams.get('edit') !== null || searchParams.has('add'),
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
    <div className="flex flex-col gap-5">
      <header className="flex items-center gap-4">
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

      {!q.isError && !q.isPending && instances.length > 0 && (
        <div className="flex flex-col gap-4">
          {instances.map((inst) => (
            <InstanceHero
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
            // Story 494 (B-13): strip both `edit=<name>` and `add=1` deep-
            // link query params so the URL is bookmarkable but the dialog
            // does not re-open on subsequent renders or page reloads.
            if (searchParams.has('edit') || searchParams.has('add')) {
              const next = new URLSearchParams(searchParams);
              next.delete('edit');
              next.delete('add');
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
