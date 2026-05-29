import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Check, AlertTriangle } from 'lucide-react';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { SkeletonRows } from '@/components/SkeletonRows';
import { useInstances } from '@/lib/instances';
import { useScans, flattenScans } from '@/lib/scans';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { relativeTime, durationMs } from '@/lib/format';
import { healthKind } from '@/lib/badge-variants';

type Variant = 'default' | 'success' | 'warning' | 'danger';

export function Dashboard() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const inst = useInstances();
  const scans = useScans();
  const failures = useGrabs({ status: 'import_failed' });

  const onRowEnter = (e: React.KeyboardEvent, target: string) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      navigate(target);
    }
  };

  const instances = inst.data?.instances ?? [];
  const healthy = instances.filter((i) => healthKind(i.health) === 'success').length;
  const unavailable = instances.filter((i) => healthKind(i.health) === 'danger').length;
  const instVariant: Variant = unavailable > 0 ? 'danger' : 'success';

  const allScans = useMemo(() => flattenScans(scans.data?.pages), [scans.data]);
  const recentScans = allScans.slice(0, 5);
  const grabsCount = allScans.reduce((a, s) => a + (s.grabs_performed ?? 0), 0);
  const failsCount = allScans.reduce((a, s) => a + (s.grabs_failed ?? 0), 0);

  const recentFailures = flattenGrabs(failures.data?.pages).slice(0, 5);

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <header className="flex items-center gap-4">
        <h1 className="text-[22px] font-semibold tracking-tight">{t('dashboard.title')}</h1>
        <span className="font-mono text-[11px] text-faint">{t('dashboard.pollingHint')}</span>
      </header>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard
          label={t('dashboard.cards.instances')}
          variant={instVariant}
          value={`${healthy}`}
          suffix={`/ ${instances.length}`}
          foot={
            unavailable
              ? t('dashboard.cards.healthyFootDown', { healthy, down: unavailable })
              : t('dashboard.cards.healthyFoot', { healthy })
          }
        />
        <StatCard
          label={t('dashboard.cards.recentScans')}
          value={allScans.length}
          foot={recentScans[0]?.started_at ? relativeTime(recentScans[0].started_at) : '—'}
        />
        <StatCard
          label={t('dashboard.cards.recentGrabs')}
          value={grabsCount}
          {...(failsCount ? { suffix: t('dashboard.cards.failSuffix', { count: failsCount }) } : {})}
          foot={
            grabsCount
              ? t('dashboard.cards.successSuffix', {
                  percent: Math.round(((grabsCount - failsCount) / Math.max(grabsCount, 1)) * 100),
                })
              : t('dashboard.cards.noGrabsYet')
          }
          variant={failsCount ? 'warning' : 'default'}
        />
        <StatCard
          label={t('dashboard.cards.recentFailures')}
          value={recentFailures.length}
          variant={recentFailures.length > 0 ? 'danger' : 'default'}
          foot={recentFailures.length === 0 ? t('dashboard.cards.noFailures') : t('dashboard.cards.importsFailing')}
        />
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">{t('dashboard.health.title')}</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('dashboard.health.name')}</TableHead>
                <TableHead>{t('dashboard.health.healthCol')}</TableHead>
                <TableHead>{t('dashboard.health.lastCheck')}</TableHead>
                <TableHead>{t('dashboard.health.transitions')}</TableHead>
                <TableHead>{t('dashboard.health.lastError')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {inst.isPending && <SkeletonRows rows={3} cols={['md', 'sm', 'md', 'sm', 'xl']} />}
              {!inst.isPending && instances.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5}>
                    <EmptyState
                      title={t('dashboard.empty.title')}
                      body={t('dashboard.empty.body')}
                    />
                  </TableCell>
                </TableRow>
              )}
              {instances.map((i) => (
                <TableRow
                  key={i.name}
                  onClick={() => navigate('/instances')}
                  onKeyDown={(e) => onRowEnter(e, '/instances')}
                  tabIndex={0}
                  role="button"
                  aria-label={t('dashboard.health.openInstance', { name: i.name })}
                  className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <TableCell className="font-mono font-medium">{i.name}</TableCell>
                  <TableCell>
                    <StatusBadge value={i.health} mode="health" />
                  </TableCell>
                  <TableCell className="text-muted">{relativeTime(i.last_check_at)}</TableCell>
                  <TableCell className="font-mono">{i.transitions_count ?? 0}</TableCell>
                  <TableCell className="text-muted text-[12px] truncate max-w-xs">
                    {i.last_error || '—'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">{t('dashboard.recent.scansTitle')}</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => navigate('/scans')}>
            {t('dashboard.recent.viewAll')}
          </Button>
        </CardHeader>
        <CardContent className="p-0">
          {scans.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>{t('dashboard.recent.loadScansFailed')}</AlertTitle>
              <AlertDescription>{scans.error.message}</AlertDescription>
            </Alert>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8"></TableHead>
                  <TableHead>{t('dashboard.recent.instance')}</TableHead>
                  <TableHead>{t('dashboard.recent.trigger')}</TableHead>
                  <TableHead>{t('dashboard.recent.started')}</TableHead>
                  <TableHead>{t('dashboard.recent.duration')}</TableHead>
                  <TableHead>{t('dashboard.recent.series')}</TableHead>
                  <TableHead>{t('dashboard.recent.grabs')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {scans.isPending && (
                  <SkeletonRows rows={5} cols={['xs', 'md', 'sm', 'md', 'sm', 'sm', 'sm']} />
                )}
                {!scans.isPending && recentScans.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={7}>
                      <EmptyState
                        title={t('scans.empty.title')}
                        body={t('dashboard.recent.noScansBody')}
                      />
                    </TableCell>
                  </TableRow>
                )}
                {recentScans.map((s) => (
                  <TableRow
                    key={s.id}
                    onClick={() => s.id && navigate(`/scans/${s.id}`)}
                    onKeyDown={(e) => s.id && onRowEnter(e, `/scans/${s.id}`)}
                    tabIndex={0}
                    role="button"
                    aria-label={t('dashboard.recent.openScan', { id: (s.id ?? '').slice(0, 8) })}
                    className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <TableCell>
                      {s.status === 'failed' ? (
                        <AlertTriangle className="w-3.5 h-3.5 text-status-danger" />
                      ) : (
                        <Check className="w-3.5 h-3.5 text-status-success" />
                      )}
                    </TableCell>
                    <TableCell className="font-mono">{s.instance}</TableCell>
                    <TableCell>
                      <StatusBadge value={s.trigger} />
                    </TableCell>
                    <TableCell className="text-muted">{relativeTime(s.started_at)}</TableCell>
                    <TableCell className="font-mono">
                      {durationMs(s.started_at, s.finished_at)}
                    </TableCell>
                    <TableCell className="font-mono">{s.series_scanned ?? 0}</TableCell>
                    <TableCell className="font-mono">{s.grabs_performed ?? 0}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">{t('dashboard.recent.failuresTitle')}</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>
            {t('dashboard.recent.viewAll')}
          </Button>
        </CardHeader>
        <CardContent className="p-0">
          {recentFailures.length === 0 && !failures.isPending ? (
            <EmptyState
              icon={<Check className="w-7 h-7 text-status-success" />}
              title={t('dashboard.recent.noFailuresTitle')}
              body={t('dashboard.recent.noFailuresBody')}
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('dashboard.recent.series')}</TableHead>
                  <TableHead>{t('dashboard.recent.status')}</TableHead>
                  <TableHead>{t('dashboard.recent.instance')}</TableHead>
                  <TableHead>{t('dashboard.recent.time')}</TableHead>
                  <TableHead>{t('dashboard.recent.errorCol')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {failures.isPending && (
                  <SkeletonRows rows={3} cols={['lg', 'sm', 'sm', 'md', '2xl']} />
                )}
                {recentFailures.map((g) => (
                  <TableRow key={g.id}>
                    <TableCell className="font-medium">{g.series_title || '—'}</TableCell>
                    <TableCell>
                      <StatusBadge value={g.status} />
                    </TableCell>
                    <TableCell className="font-mono">{g.instance}</TableCell>
                    <TableCell className="text-muted">
                      {relativeTime(g.updated_at ?? g.created_at)}
                    </TableCell>
                    <TableCell className="text-muted text-[12px] truncate max-w-md font-mono">
                      {g.error_message || '—'}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
