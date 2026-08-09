import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, Bell, Loader2, Pencil, Plus, Send, Trash2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { AgentFormDialog } from '@/components/settings/AgentFormDialog';
import { type NotificationAgentView } from '@/api/notificationAgents';
import { useAgents, useDeleteAgent, useTestAgent } from '@/lib/notification-agents-mutations';

function AgentRow({
  agent,
  onEdit,
  onDelete,
  onTest,
  testing,
}: {
  readonly agent: NotificationAgentView;
  readonly onEdit: (a: NotificationAgentView) => void;
  readonly onDelete: (a: NotificationAgentView) => void;
  readonly onTest: (a: NotificationAgentView) => void;
  readonly testing: boolean;
}) {
  const { t } = useTranslation();
  const eventCount = agent.event_types?.length ?? 0;
  return (
    <section
      data-testid={`agent-row-${agent.id}`}
      className="flex items-center gap-3 p-4 rounded-md bg-bg-surface border border-border-faint"
    >
      <Bell className="w-4 h-4 text-tx-muted shrink-0" aria-hidden="true" />
      <div className="flex flex-col gap-1 min-w-0">
        <div className="flex items-center gap-2">
          <h3 className="text-[14px] font-[650] tracking-[-0.01em] m-0 truncate">{agent.name}</h3>
          {agent.enabled ? (
            <Badge variant="ok" data-testid={`agent-enabled-${agent.id}`}>
              {t('settings.agents.enabledBadge')}
            </Badge>
          ) : (
            <Badge variant="neutral" data-testid={`agent-disabled-${agent.id}`}>
              {t('settings.agents.disabledBadge')}
            </Badge>
          )}
        </div>
        <div className="flex items-center gap-2 text-[11.5px] text-tx-faint">
          {agent.scheme && (
            <span className="font-mono" data-testid={`agent-scheme-${agent.id}`}>
              {agent.scheme}
            </span>
          )}
          <span data-testid={`agent-event-count-${agent.id}`}>
            {t('settings.agents.eventCount', { count: eventCount })}
          </span>
        </div>
      </div>

      <div className="ml-auto flex items-center gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onTest(agent)}
          disabled={testing || !agent.configured}
          data-testid={`agent-test-${agent.id}`}
        >
          {testing ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
          ) : (
            <Send className="w-3.5 h-3.5" />
          )}
          <span className="ml-1.5">{t('settings.agents.test')}</span>
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onEdit(agent)}
          data-testid={`agent-edit-${agent.id}`}
          aria-label={t('common.edit')}
        >
          <Pencil className="w-3.5 h-3.5" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onDelete(agent)}
          data-testid={`agent-delete-${agent.id}`}
          aria-label={t('common.delete')}
        >
          <Trash2 className="w-3.5 h-3.5 text-status-danger" />
        </Button>
      </div>
    </section>
  );
}

export function AgentsTab() {
  const { t } = useTranslation();
  const q = useAgents();
  const del = useDeleteAgent();
  const test = useTestAgent();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<NotificationAgentView | null>(null);
  const [deleting, setDeleting] = useState<NotificationAgentView | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);

  const openCreate = () => {
    setEditing(null);
    setDialogOpen(true);
  };
  const openEdit = (a: NotificationAgentView) => {
    setEditing(a);
    setDialogOpen(true);
  };
  const onTest = (a: NotificationAgentView) => {
    if (a.id == null) return;
    setTestingId(a.id);
    test.mutate(a.id, { onSettled: () => setTestingId(null) });
  };
  const confirmDelete = async () => {
    if (deleting?.id == null) return;
    await del.mutateAsync(deleting.id);
    setDeleting(null);
  };

  return (
    <div className="flex flex-col gap-5 max-w-[760px]" data-testid="settings-agents">
      <header className="flex items-start gap-4">
        <div>
          <h1 className="text-[18px] font-[650] tracking-[-0.01em] m-0">
            {t('settings.agents.title')}
          </h1>
          <p className="text-[13px] text-muted">{t('settings.agents.subtitle')}</p>
        </div>
        <Button onClick={openCreate} className="ml-auto gap-1.5" variant="primary" data-testid="agent-add">
          <Plus className="w-3.5 h-3.5" />
          {t('settings.agents.add')}
        </Button>
      </header>

      {q.isLoading && (
        <div className="flex flex-col gap-3" data-testid="agents-loading">
          <Skeleton className="h-[76px] w-full rounded-md" />
          <Skeleton className="h-[76px] w-full rounded-md" />
        </div>
      )}

      {q.isError && (
        <Alert variant="destructive" data-testid="agents-load-err">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('settings.agents.loadErr')}</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isLoading && !q.isError && q.data && q.data.length === 0 && (
        <p className="text-[13px] text-tx-faint" data-testid="agents-empty">
          {t('settings.agents.empty')}
        </p>
      )}

      {!q.isLoading && !q.isError && q.data && q.data.length > 0 && (
        <div className="flex flex-col gap-3">
          {q.data.map((a) => (
            <AgentRow
              key={a.id}
              agent={a}
              onEdit={openEdit}
              onDelete={setDeleting}
              onTest={onTest}
              testing={testingId === a.id}
            />
          ))}
        </div>
      )}

      <AgentFormDialog
        open={dialogOpen}
        onOpenChange={(v) => {
          setDialogOpen(v);
          if (!v) setEditing(null);
        }}
        mode={editing ? 'edit' : 'create'}
        agent={editing ?? undefined}
      />

      <Dialog open={Boolean(deleting)} onOpenChange={(v) => !v && setDeleting(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{t('settings.agents.delete')}</DialogTitle>
            <DialogDescription>
              {t('settings.agents.deleteConfirm', { name: deleting?.name ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleting(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={confirmDelete}
              disabled={del.isPending}
              data-testid="agent-delete-confirm"
            >
              {del.isPending && <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />}
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
