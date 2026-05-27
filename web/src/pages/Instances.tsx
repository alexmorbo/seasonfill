import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Plus, Pencil, Trash2, AlertTriangle, ListOrdered } from 'lucide-react';
import { toast } from 'sonner';
import { useInstances, type Instance } from '@/lib/instances';
import { useDeleteInstance, useInstanceDetail } from '@/lib/instances-mutations';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { InstanceFormDialog } from '@/components/settings/InstanceFormDialog';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';
import { KIND_CLASS, KIND_DOT, statusKind } from '@/lib/badge-variants';

export function Instances() {
  const { t } = useTranslation();
  const q = useInstances();
  const del = useDeleteInstance();
  const instances = q.data?.instances ?? [];

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const detailQuery = useInstanceDetail(editing);
  const editDetail = detailQuery.data?.detail;
  // Pin dependencies on primitives so a 5s background refetch returning
  // a structurally-identical detail does NOT mint a fresh object into
  // the dialog's `initial` prop. Mirrors the comment from the original
  // InstancesTab (and the reasoning baked into 033a + 028b).
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

  const openCreate = () => {
    setEditing(null);
    setDialogOpen(true);
  };
  const openEdit = (name: string) => {
    setEditing(name);
    setDialogOpen(true);
  };

  const onDeleteClick = (name: string) => {
    if (instances.length <= 1) {
      toast.error(t('instances.cannotDeleteLast'));
      return;
    }
    setDeleting(name);
  };

  const confirmDelete = async () => {
    if (!deleting) return;
    await del.mutateAsync({ name: deleting });
    setDeleting(null);
  };

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <header className="flex items-center gap-4">
        <h1 className="text-[22px] font-semibold tracking-tight">{t('instances.title')}</h1>
        <Button
          onClick={openCreate}
          className="ml-auto gap-1.5"
          aria-label={t('instances.actions.addAria')}
        >
          <Plus className="w-3.5 h-3.5" /> {t('settings.instances.add')}
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
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {[0, 1, 2].map((i) => (
            <Card key={i}>
              <CardContent className="p-5 flex flex-col gap-2.5">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-3 w-48" />
                <Skeleton className="h-3 w-40" />
                <Skeleton className="h-3 w-36" />
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {!q.isError && !q.isPending && instances.length === 0 && (
        <Card>
          <CardContent className="p-0">
            <EmptyState
              title={t('instances.empty.title')}
              body={t('instances.empty.body')}
            />
          </CardContent>
        </Card>
      )}
      {instances.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {instances.map((inst: Instance) => (
            <Card key={inst.name} className="relative group">
              <span
                className={cn(
                  'absolute top-4 right-4 w-2 h-2 rounded-full',
                  KIND_DOT[statusKind(inst.health)],
                )}
                aria-hidden="true"
              />
              <div
                className={cn(
                  'absolute top-3 right-9 flex items-center gap-0.5',
                  'opacity-60 group-hover:opacity-100 focus-within:opacity-100',
                  'transition-opacity',
                )}
              >
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={t('settings.instances.editAria', { name: inst.name })}
                  onClick={() => inst.name && openEdit(inst.name)}
                >
                  <Pencil className="w-3.5 h-3.5" />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={t('settings.instances.deleteAria', { name: inst.name })}
                  onClick={() => inst.name && onDeleteClick(inst.name)}
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </Button>
              </div>
              <CardHeader className="pb-2">
                <h3 className="text-[15px] font-semibold tracking-tight flex items-center gap-2">
                  <span className="font-mono">{inst.name}</span>
                  {inst.health && inst.health !== 'available' && (
                    <StatusBadge value={inst.health} />
                  )}
                </h3>
              </CardHeader>
              <CardContent className="text-[13px] flex flex-col gap-3">
                <dl className="grid grid-cols-[110px_1fr] gap-y-1.5 gap-x-3 text-[12.5px]">
                  <dt className="text-faint">{t('dashboard.health.healthCol')}</dt>
                  <dd className="font-mono">{inst.health ?? t('common.unknown').toLowerCase()}</dd>
                  <dt className="text-faint">{t('instances.columns.mode')}</dt>
                  <dd>
                    <span
                      className={cn(
                        'inline-flex items-center px-1.5 h-[18px] rounded border font-mono text-[10.5px]',
                        KIND_CLASS[inst.mode === 'manual' ? 'warning' : 'neutral'],
                      )}
                      data-testid={`mode-${inst.name}`}
                    >
                      {inst.mode ?? 'auto'}
                    </span>
                  </dd>
                  <dt className="text-faint">{t('dashboard.health.lastCheck')}</dt>
                  <dd className="text-muted">{relativeTime(inst.last_check_at)}</dd>
                  <dt className="text-faint">{t('dashboard.health.transitions')}</dt>
                  <dd
                    className={cn(
                      'font-mono',
                      (inst.transitions_count ?? 0) > 0 && 'text-status-warning',
                    )}
                  >
                    {inst.transitions_count ?? 0}
                  </dd>
                  {inst.last_error && (
                    <>
                      <dt className="text-faint">{t('dashboard.health.lastError')}</dt>
                      <dd className="text-muted font-mono text-[11.5px] break-all">
                        {inst.last_error}
                      </dd>
                    </>
                  )}
                </dl>
                {inst.name && (
                  <Link
                    to={`/instances/${encodeURIComponent(inst.name)}/queue`}
                    className="inline-flex items-center gap-1.5 text-[12px] font-medium text-accent hover:underline self-start"
                    aria-label={t('instances.openQueueAria', { name: inst.name })}
                  >
                    <ListOrdered className="w-3.5 h-3.5" />
                    {inst.mode === 'manual' ? t('instances.actions.openQueue') : t('instances.actions.viewQueue')} →
                  </Link>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <InstanceFormDialog
        open={dialogOpen}
        onOpenChange={(v) => {
          setDialogOpen(v);
          if (!v) setEditing(null);
        }}
        mode={editing ? 'edit' : 'create'}
        initial={editing ? editInitial : undefined}
      />

      <Dialog open={Boolean(deleting)} onOpenChange={(v) => !v && setDeleting(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{t('instances.delete.title', { name: deleting })}</DialogTitle>
            <DialogDescription>
              {t('instances.delete.body')}
            </DialogDescription>
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
