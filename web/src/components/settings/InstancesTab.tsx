import { useState } from 'react';
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
  const list = useInstances();
  const del = useDeleteInstance();
  const instances = list.data?.instances ?? [];

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const detailQuery = useInstanceDetail(editing);
  const editInitial = detailQuery.data?.detail
    ? {
        name: detailQuery.data.detail.name ?? '',
        url: detailQuery.data.detail.url ?? '',
        mode: (detailQuery.data.detail.mode as 'auto' | 'manual' | undefined) ?? 'auto',
      }
    : undefined;

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
      toast.error('Cannot delete the last Sonarr instance');
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
        <h2 className="text-[15px] font-semibold tracking-tight">Sonarr instances</h2>
        <Button onClick={openCreate} className="gap-1.5">
          <Plus className="w-3.5 h-3.5" /> Add instance
        </Button>
      </div>

      <div className="border border-border rounded-md overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>URL</TableHead>
              <TableHead>Mode</TableHead>
              <TableHead>Health</TableHead>
              <TableHead className="w-[120px] text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {instances.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted py-6">
                  {list.isPending ? 'Loading…' : 'No instances yet.'}
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
                  {inst.health ?? 'unknown'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm" variant="ghost" aria-label={`Edit ${inst.name}`}
                    onClick={() => inst.name && openEdit(inst.name)}
                  >
                    <Pencil className="w-3.5 h-3.5" />
                  </Button>
                  <Button
                    size="sm" variant="ghost" aria-label={`Delete ${inst.name}`}
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
            <DialogTitle>Delete instance &quot;{deleting}&quot;?</DialogTitle>
            <DialogDescription>
              This removes the instance, its encrypted api_key, and all
              series-scope cooldowns, scans, decisions, and grab records
              keyed on this instance name. Cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleting(null)}>Cancel</Button>
            <Button
              variant="destructive"
              onClick={confirmDelete}
              disabled={del.isPending}
            >
              {del.isPending ? 'Deleting…' : 'Delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
