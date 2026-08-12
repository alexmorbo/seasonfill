import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2, Check, X, TriangleAlert } from 'lucide-react';

import {
  useRequests, useApproveRequest, useDenyRequest,
  type RequestItem, type RequestStatus, type RequestMediaType,
} from '@/api/requests';
import { useMe } from '@/hooks/useMe';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useFormatDate } from '@/lib/timezone';
import { ApiError } from '@/lib/api';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';

// Pending rows sort ahead of approved/denied so the operator's actionable
// queue is always at the top. Within each bucket, newest-created first.
const STATUS_RANK: Record<string, number> = { pending: 0, approved: 1, denied: 2 };

function sortRequests(items: readonly RequestItem[]): RequestItem[] {
  return [...items].sort((a, b) => {
    const ra = STATUS_RANK[a.status ?? ''] ?? 3;
    const rb = STATUS_RANK[b.status ?? ''] ?? 3;
    if (ra !== rb) return ra - rb;
    return (b.created_at ?? '').localeCompare(a.created_at ?? '');
  });
}

function MediaTypeBadge({ type }: { type: string | undefined }) {
  const { t } = useTranslation();
  const media = (type === 'movie' ? 'movie' : 'tv') as RequestMediaType;
  return (
    <Badge variant={media === 'movie' ? 'accent' : 'info'} data-testid={`request-type-${media}`}>
      {t(`requests.mediaType.${media}`)}
    </Badge>
  );
}

function StatusBadge({ status }: { status: string | undefined }) {
  const { t } = useTranslation();
  const s = (STATUS_RANK[status ?? ''] !== undefined ? status : 'pending') as RequestStatus;
  const variant = s === 'approved' ? 'ok' : s === 'denied' ? 'danger' : 'warn';
  return (
    <Badge variant={variant} data-testid={`request-status-${s}`}>
      {t(`requests.status.${s}`)}
    </Badge>
  );
}

// Per-row Approve / Deny actions. Each button opens its own confirm dialog
// (mirrors CancelScanDialog). Approve WARNS that it adds to the library;
// Deny is the safe path. Buttons disable while any mutation is in flight.
function RowActions({ row }: { row: RequestItem }) {
  const { t } = useTranslation();
  const [confirm, setConfirm] = useState<null | 'approve' | 'deny'>(null);
  const approve = useApproveRequest();
  const deny = useDenyRequest();
  const busy = approve.isPending || deny.isPending;
  const id = row.id ?? 0;
  const label = row.title ?? `#${row.tmdb_id ?? ''}`;

  const onError = (err: unknown) => {
    const msg = err instanceof ApiError ? err.message : String(err);
    toast.error(t('requests.toast.error', { error: msg }));
  };

  const doApprove = () => {
    approve.mutate(id, {
      onSuccess: () => toast.success(t('requests.toast.approved', { title: label })),
      onError,
      onSettled: () => setConfirm(null),
    });
  };
  const doDeny = () => {
    deny.mutate(id, {
      onSuccess: () => toast.success(t('requests.toast.denied', { title: label })),
      onError,
      onSettled: () => setConfirm(null),
    });
  };

  return (
    <div className="flex items-center justify-end gap-2">
      <Button
        type="button" variant="outline" size="sm"
        disabled={busy}
        onClick={() => setConfirm('approve')}
        data-testid={`request-approve-${id}`}
        className="h-7 text-[12px] border-status-success/50 text-status-success hover:bg-status-success/10"
      >
        <Check className="w-3.5 h-3.5 mr-1" />
        {t('requests.actions.approve')}
      </Button>
      <Button
        type="button" variant="outline" size="sm"
        disabled={busy}
        onClick={() => setConfirm('deny')}
        data-testid={`request-deny-${id}`}
        className="h-7 text-[12px] border-status-danger/50 text-status-danger hover:bg-status-danger/10"
      >
        <X className="w-3.5 h-3.5 mr-1" />
        {t('requests.actions.deny')}
      </Button>

      <Dialog open={confirm === 'approve'} onOpenChange={(o) => !o && setConfirm(null)}>
        <DialogContent data-testid="request-approve-dialog">
          <DialogHeader>
            <DialogTitle>{t('requests.approveDialog.title')}</DialogTitle>
            <DialogDescription>
              {t('requests.approveDialog.body', { title: label })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirm(null)} disabled={approve.isPending}>
              {t('requests.approveDialog.cancel')}
            </Button>
            <Button
              variant="default"
              onClick={doApprove}
              disabled={approve.isPending}
              data-testid={`request-approve-confirm-${id}`}
            >
              {approve.isPending && <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />}
              {t('requests.approveDialog.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirm === 'deny'} onOpenChange={(o) => !o && setConfirm(null)}>
        <DialogContent data-testid="request-deny-dialog">
          <DialogHeader>
            <DialogTitle>{t('requests.denyDialog.title')}</DialogTitle>
            <DialogDescription>
              {t('requests.denyDialog.body', { title: label })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirm(null)} disabled={deny.isPending}>
              {t('requests.denyDialog.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={doDeny}
              disabled={deny.isPending}
              data-testid={`request-deny-confirm-${id}`}
            >
              {deny.isPending && <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />}
              {t('requests.denyDialog.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function RequestRow({ row }: { row: RequestItem }) {
  const formatDate = useFormatDate();
  const requester = row.username && row.username.length > 0
    ? row.username
    : `#${row.user_id ?? ''}`;
  const title = row.title ?? `#${row.tmdb_id ?? ''}`;
  const isTv = row.media_type !== 'movie';
  const seasons = row.seasons ?? [];
  const isPending = (row.status ?? 'pending') === 'pending';

  return (
    <TableRow data-testid={`request-row-${row.id ?? ''}`}>
      <TableCell className="font-medium">{requester}</TableCell>
      <TableCell><MediaTypeBadge type={row.media_type} /></TableCell>
      <TableCell className="max-w-[280px] truncate" title={title}>{title}</TableCell>
      <TableCell className="text-tx-secondary tabular-nums" data-testid={`request-seasons-${row.id ?? ''}`}>
        {isTv ? (seasons.length > 0 ? seasons.join(', ') : '—') : ''}
      </TableCell>
      <TableCell><StatusBadge status={row.status} /></TableCell>
      <TableCell className="text-tx-muted whitespace-nowrap">
        {formatDate(row.created_at, 'datetime')}
      </TableCell>
      <TableCell className="text-right">
        {isPending ? <RowActions row={row} /> : null}
      </TableCell>
    </TableRow>
  );
}

// Requests — the /requests admin page. Lists media requests (pending first)
// with per-row Approve / Deny confirm actions. Approve adds the title to the
// library; Deny is the safe no-op.
//
// GATING: admin-only. Non-admins get a localized denied panel and the nav
// entry is hidden (see AppSidebar). U-6b will surface bool-permissions in
// /me so a non-admin "request manager" can also approve — DO NOT add perms
// to /me here; this gate is intentionally role === 'admin' only for U-6a.
export function Requests() {
  const { t } = useTranslation();
  useSetPageTitle(t('requests.title'));
  const me = useMe();
  const allowed = me.data?.role === 'admin';
  const q = useRequests();

  const rows = useMemo(() => sortRequests(q.data ?? []), [q.data]);

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
        data-testid="requests-access-denied"
        role="alert"
        aria-live="polite"
        className="flex flex-col items-center justify-center gap-2 min-h-[160px] rounded-md border border-border-faint bg-surface/40 p-5 text-center"
      >
        <p className="text-[14px] font-medium text-tx-primary">
          {t('requests.accessDenied')}
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="requests-page">
      <header>
        <h1 className="text-[18px] font-[650] tracking-[-0.01em] m-0">
          {t('requests.title')}
        </h1>
        <p className="text-[13px] text-muted">{t('requests.subtitle')}</p>
      </header>

      {q.isLoading && (
        <div className="flex flex-col gap-3" data-testid="requests-loading">
          <Skeleton className="h-10 w-full rounded-md" />
          <Skeleton className="h-10 w-full rounded-md" />
          <Skeleton className="h-10 w-full rounded-md" />
        </div>
      )}

      {q.isError && (
        <Alert variant="destructive" data-testid="requests-load-err">
          <TriangleAlert className="w-4 h-4" />
          <AlertTitle>{t('requests.loadErr')}</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isLoading && !q.isError && rows.length === 0 && (
        <p className="text-[13px] text-tx-faint" data-testid="requests-empty">
          {t('requests.empty')}
        </p>
      )}

      {!q.isLoading && !q.isError && rows.length > 0 && (
        <Table data-testid="requests-table">
          <TableHeader>
            <TableRow>
              <TableHead>{t('requests.columns.requester')}</TableHead>
              <TableHead>{t('requests.columns.type')}</TableHead>
              <TableHead>{t('requests.columns.title')}</TableHead>
              <TableHead>{t('requests.columns.seasons')}</TableHead>
              <TableHead>{t('requests.columns.status')}</TableHead>
              <TableHead>{t('requests.columns.created')}</TableHead>
              <TableHead className="text-right">{t('requests.columns.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <RequestRow key={row.id ?? `${row.user_id}-${row.tmdb_id}`} row={row} />
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
