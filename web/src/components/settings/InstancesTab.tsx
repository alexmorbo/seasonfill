import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Plus, Pencil, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { useInstances, type Instance } from '@/lib/instances';
import {
  useDeleteInstance, useInstanceDetail,
} from '@/lib/instances-mutations';
import { InstanceFormDialog } from './InstanceFormDialog';

export function InstancesTab() {
  const { t } = useTranslation();
  const list = useInstances();
  const del = useDeleteInstance();
  const instances = list.data?.instances ?? [];

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const detailQuery = useInstanceDetail(editing);
  const editDetail = detailQuery.data?.detail;
  // Pin dependencies on primitive fields so a 5s background refetch
  // that returns a structurally-identical detail does NOT mint a fresh
  // object reference into the dialog's `initial` prop. The dialog's
  // reset() effect keys on `initial?.name`, but downstream useMemo()s
  // (e.g. for form defaults) benefit from stable identity too.
  const detailName = editDetail?.name;
  const detailUrl = editDetail?.url;
  const detailMode = editDetail?.mode;
  const editInitial = useMemo(() => {
    if (!editDetail) return undefined;
    return {
      name: detailName ?? '',
      url: detailUrl ?? '',
      mode: (detailMode as 'auto' | 'manual' | undefined) ?? 'auto',
    };
    // editDetail is the existence guard; primitives drive identity.
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
      toast.error(t('settings.instances.cannotDeleteLast'));
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
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-[15px] font-semibold tracking-tight">{t('instances.title')}</h2>
        <Button onClick={openCreate} className="gap-1.5">
          <Plus className="w-3.5 h-3.5" /> {t('settings.instances.add')}
        </Button>
      </div>

      <div className="border border-border rounded-md overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('instances.columns.name')}</TableHead>
              <TableHead>{t('instances.columns.url')}</TableHead>
              <TableHead>{t('instances.columns.mode')}</TableHead>
              <TableHead>{t('dashboard.health.healthCol')}</TableHead>
              <TableHead className="w-[120px] text-right">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {instances.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted py-6">
                  {list.isPending ? t('common.loading') : t('settings.instances.none')}
                </TableCell>
              </TableRow>
            )}
            {instances.map((inst: Instance) => (
              <TableRow key={inst.name}>
                <TableCell className="font-mono">{inst.name}</TableCell>
                <TableCell className="font-mono text-[12px] text-muted">
                  {inst.url ?? ''}
                </TableCell>
                <TableCell>{inst.mode ?? 'auto'}</TableCell>
                <TableCell className="font-mono text-[12px]">
                  {inst.health ?? t('common.unknown').toLowerCase()}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm" variant="ghost" aria-label={t('settings.instances.editAria', { name: inst.name })}
                    onClick={() => inst.name && openEdit(inst.name)}
                  >
                    <Pencil className="w-3.5 h-3.5" />
                  </Button>
                  <Button
                    size="sm" variant="ghost" aria-label={t('settings.instances.deleteAria', { name: inst.name })}
                    onClick={() => inst.name && onDeleteClick(inst.name)}
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

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
            <DialogTitle>{t('settings.instances.deleteTitle', { name: deleting })}</DialogTitle>
            <DialogDescription>
              {t('settings.instances.deleteBody')}
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
