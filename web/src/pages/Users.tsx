import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2, TriangleAlert, Trash2 } from 'lucide-react';

import {
  useUsers, usePatchUser, useDeleteUser,
  PERM_KEYS, type UserItem, type UserPatch, type PermKey, type UserRole, type AuthSource,
} from '@/api/users';
import { useMe } from '@/hooks/useMe';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useFormatDate } from '@/lib/timezone';
import { ApiError } from '@/lib/api';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';

const PERM_LABEL: Record<PermKey, string> = {
  auto_approve: 'autoApprove',
  request: 'request',
  manage_requests: 'manageRequests',
  manage_users: 'manageUsers',
  request_4k: 'request4k',
};

function sortUsers(items: readonly UserItem[]): UserItem[] {
  return [...items].sort((a, b) =>
    (a.username ?? '').localeCompare(b.username ?? ''),
  );
}

function useErrToast(): (err: unknown) => string {
  const { t } = useTranslation();
  return (err: unknown) => {
    const code = err instanceof ApiError
      ? (err.body as { code?: string } | undefined)?.code
      : undefined;
    if (code === 'LAST_ADMIN') return t('users.toast.lastAdmin');
    if (code === 'SELF_LOCKOUT') return t('users.toast.selfLockout');
    if (code === 'INVALID_ROLE') return t('users.toast.invalidRole');
    const msg = err instanceof ApiError ? err.message : String(err);
    return t('users.toast.error', { error: msg });
  };
}

function RoleBadge({ role }: { role: string | undefined }) {
  const { t } = useTranslation();
  const r = (role === 'admin' ? 'admin' : 'user') as UserRole;
  return (
    <Badge variant={r === 'admin' ? 'accent' : 'default'}>
      {t(`users.role.${r}`)}
    </Badge>
  );
}

function AuthSourceBadge({ source }: { source: string | undefined }) {
  const { t } = useTranslation();
  const s = (source === 'oidc' || source === 'jellyfin' ? source : 'forms') as AuthSource;
  return (
    <Badge variant="info" data-testid={`user-authsource-${s}`}>
      {t(`users.authSource.${s}`)}
    </Badge>
  );
}

function DeleteUserDialog({ row, isSelf }: { row: UserItem; isSelf: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const del = useDeleteUser();
  const errToast = useErrToast();
  const id = row.id ?? 0;
  const label = row.username ?? `#${id}`;

  const doDelete = () => {
    del.mutate(id, {
      onSuccess: () => toast.success(t('users.toast.deleted', { username: label })),
      onError: (err) => toast.error(errToast(err)),
      onSettled: () => setOpen(false),
    });
  };

  return (
    <>
      <Button
        type="button" variant="outline" size="sm"
        disabled={isSelf}
        title={isSelf ? t('users.selfRowHint') : undefined}
        onClick={() => setOpen(true)}
        data-testid={`user-delete-${id}`}
        className="h-7 text-[12px] border-status-danger/50 text-status-danger hover:bg-status-danger/10 disabled:opacity-40"
      >
        <Trash2 className="w-3.5 h-3.5 mr-1" />
        {t('users.actions.delete')}
      </Button>

      <Dialog open={open} onOpenChange={(o) => !o && setOpen(false)}>
        <DialogContent data-testid="user-delete-dialog">
          <DialogHeader>
            <DialogTitle>{t('users.deleteDialog.title')}</DialogTitle>
            <DialogDescription>
              {t('users.deleteDialog.body', { username: label })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={del.isPending}>
              {t('users.deleteDialog.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={doDelete}
              disabled={del.isPending}
              data-testid={`user-delete-confirm-${id}`}
            >
              {del.isPending && <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />}
              {t('users.deleteDialog.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function UserRow({ row, selfId }: { row: UserItem; selfId: number | undefined }) {
  const { t } = useTranslation();
  const formatDate = useFormatDate();
  const patch = usePatchUser();
  const errToast = useErrToast();
  const id = row.id ?? 0;
  const isSelf = selfId !== undefined && selfId === id;
  const isAdmin = row.role === 'admin';

  const applyPatch = (body: UserPatch) => {
    patch.mutate(
      { id, patch: body },
      {
        onSuccess: () => toast.success(t('users.toast.updated')),
        onError: (err) => toast.error(errToast(err)),
      },
    );
  };

  return (
    <TableRow data-testid={`user-row-${id}`}>
      <TableCell className="font-medium">{row.username ?? `#${id}`}</TableCell>
      <TableCell className="text-tx-secondary max-w-[200px] truncate" title={row.email ?? ''}>
        {row.email ?? '—'}
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <RoleBadge role={row.role} />
          <Switch
            checked={isAdmin}
            disabled={isSelf}
            aria-label={t('users.columns.role')}
            title={isSelf ? t('users.selfRowHint') : undefined}
            onCheckedChange={(next) => applyPatch({ role: next ? 'admin' : 'user' })}
            data-testid={`user-role-${id}`}
          />
        </div>
      </TableCell>
      <TableCell><AuthSourceBadge source={row.auth_source} /></TableCell>

      {PERM_KEYS.map((key) => {
        const lockSelf = isSelf && key === 'manage_users';
        return (
          <TableCell key={key} className="text-center">
            <Switch
              checked={row.permissions?.[key] === true}
              disabled={lockSelf}
              aria-label={t(`users.columns.permissions.${PERM_LABEL[key]}`)}
              title={lockSelf ? t('users.selfRowHint') : undefined}
              onCheckedChange={(next) => applyPatch({ [key]: next } as UserPatch)}
              data-testid={`user-perm-${key}-${id}`}
            />
          </TableCell>
        );
      })}

      <TableCell className="text-tx-muted whitespace-nowrap">
        {row.last_login_at ? formatDate(row.last_login_at, 'datetime') : '—'}
      </TableCell>
      <TableCell className="text-right">
        <DeleteUserDialog row={row} isSelf={isSelf} />
      </TableCell>
    </TableRow>
  );
}

export function Users() {
  const { t } = useTranslation();
  useSetPageTitle(t('users.title'));
  const me = useMe();
  const allowed = me.data?.permissions?.manage_users === true || me.data?.role === 'admin';
  const q = useUsers();

  const rows = useMemo(() => sortUsers(q.data ?? []), [q.data]);

  if (me.isLoading) {
    return (
      <div className="grid place-items-center h-32 text-faint mono">
        {t('common.checkingSession')}
      </div>
    );
  }

  if (!allowed) {
    return (
      <div
        data-testid="users-access-denied"
        role="alert"
        aria-live="polite"
        className="flex flex-col items-center justify-center gap-2 min-h-[160px] rounded-md border border-border-faint bg-surface/40 p-5 text-center"
      >
        <p className="text-[14px] font-medium text-tx-primary">
          {t('users.accessDenied')}
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="users-page">
      <header>
        <h1 className="text-[18px] font-[650] tracking-[-0.01em] m-0">
          {t('users.title')}
        </h1>
        <p className="text-[13px] text-muted">{t('users.subtitle')}</p>
      </header>

      {q.isLoading && (
        <div className="flex flex-col gap-3" data-testid="users-loading">
          <Skeleton className="h-10 w-full rounded-md" />
          <Skeleton className="h-10 w-full rounded-md" />
          <Skeleton className="h-10 w-full rounded-md" />
        </div>
      )}

      {q.isError && (
        <Alert variant="destructive" data-testid="users-load-err">
          <TriangleAlert className="w-4 h-4" />
          <AlertTitle>{t('users.loadErr')}</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isLoading && !q.isError && rows.length === 0 && (
        <p className="text-[13px] text-tx-faint" data-testid="users-empty">
          {t('users.empty')}
        </p>
      )}

      {!q.isLoading && !q.isError && rows.length > 0 && (
        <div className="overflow-x-auto">
          <Table data-testid="users-table">
            <TableHeader>
              <TableRow>
                <TableHead>{t('users.columns.username')}</TableHead>
                <TableHead>{t('users.columns.email')}</TableHead>
                <TableHead>{t('users.columns.role')}</TableHead>
                <TableHead>{t('users.columns.authSource')}</TableHead>
                {PERM_KEYS.map((key) => (
                  <TableHead key={key} className="text-center">
                    {t(`users.columns.permissions.${PERM_LABEL[key]}`)}
                  </TableHead>
                ))}
                <TableHead>{t('users.columns.lastLogin')}</TableHead>
                <TableHead className="text-right">{t('users.columns.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <UserRow key={row.id ?? row.username} row={row} selfId={me.data?.id} />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
