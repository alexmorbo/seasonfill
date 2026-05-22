import { useMemo } from 'react';
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

type Variant = 'default' | 'success' | 'warning' | 'danger';

export function Dashboard() {
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
  const healthy = instances.filter((i) => i.health === 'available').length;
  const degraded = instances.filter((i) => i.health === 'degraded').length;
  const unavailable = instances.filter((i) => i.health === 'unavailable').length;
  const instVariant: Variant = unavailable > 0 ? 'danger' : degraded > 0 ? 'warning' : 'success';

  const allScans = useMemo(() => flattenScans(scans.data?.pages), [scans.data]);
  const recentScans = allScans.slice(0, 5);
  const grabsCount = allScans.reduce((a, s) => a + (s.grabs_performed ?? 0), 0);
  const failsCount = allScans.reduce((a, s) => a + (s.grabs_failed ?? 0), 0);

  const recentFailures = flattenGrabs(failures.data?.pages).slice(0, 5);

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <header className="flex items-center gap-4">
        <h1 className="text-[22px] font-semibold tracking-tight">Dashboard</h1>
        <span className="font-mono text-[11px] text-faint">polling every 5s</span>
      </header>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard
          label="Instances"
          variant={instVariant}
          value={`${healthy + degraded}`}
          suffix={`/ ${instances.length}`}
          foot={`${healthy} healthy · ${degraded} degraded${unavailable ? ` · ${unavailable} down` : ''}`}
        />
        <StatCard
          label="Recent scans"
          value={allScans.length}
          foot={recentScans[0]?.started_at ? relativeTime(recentScans[0].started_at) : '—'}
        />
        <StatCard
          label="Recent grabs"
          value={grabsCount}
          {...(failsCount ? { suffix: `/ ${failsCount} fail` } : {})}
          foot={
            grabsCount
              ? `${Math.round(((grabsCount - failsCount) / Math.max(grabsCount, 1)) * 100)}% success`
              : 'no grabs yet'
          }
          variant={failsCount ? 'warning' : 'default'}
        />
        <StatCard
          label="Recent failures"
          value={recentFailures.length}
          variant={recentFailures.length > 0 ? 'danger' : 'default'}
          foot={recentFailures.length === 0 ? 'no failures in latest activity' : 'imports failing'}
        />
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">Instance health</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Health</TableHead>
                <TableHead>Last check</TableHead>
                <TableHead>Transitions</TableHead>
                <TableHead>Last error</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {inst.isPending && <SkeletonRows rows={3} cols={['md', 'sm', 'md', 'sm', 'xl']} />}
              {!inst.isPending && instances.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5}>
                    <EmptyState
                      title="No instances configured"
                      body="Add Sonarr instances via helm/values.yaml."
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
                  aria-label={`Open instance ${i.name}`}
                  className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <TableCell className="font-mono font-medium">{i.name}</TableCell>
                  <TableCell>
                    <StatusBadge value={i.health} />
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
          <CardTitle className="text-[14px] font-semibold">Recent scans</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => navigate('/scans')}>
            View all →
          </Button>
        </CardHeader>
        <CardContent className="p-0">
          {scans.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>Failed to load scans</AlertTitle>
              <AlertDescription>{scans.error.message}</AlertDescription>
            </Alert>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8"></TableHead>
                  <TableHead>Instance</TableHead>
                  <TableHead>Trigger</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead>Dur</TableHead>
                  <TableHead>Series</TableHead>
                  <TableHead>Grabs</TableHead>
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
                        title="No scans yet"
                        body="Trigger a scan from the New Scan button."
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
                    aria-label={`Open scan ${(s.id ?? '').slice(0, 8)}`}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
          <CardTitle className="text-[14px] font-semibold">Recent grab failures</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>
            View all →
          </Button>
        </CardHeader>
        <CardContent className="p-0">
          {recentFailures.length === 0 && !failures.isPending ? (
            <EmptyState
              icon={<Check className="w-7 h-7 text-status-success" />}
              title="No recent failures"
              body="Everything imported cleanly."
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Series</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Instance</TableHead>
                  <TableHead>Time</TableHead>
                  <TableHead>Error</TableHead>
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
