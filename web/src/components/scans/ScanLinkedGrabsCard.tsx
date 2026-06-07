import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Card, CardHeader, CardContent, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { StatusBadge } from '@/components/StatusBadge';
import { relativeTime } from '@/lib/format';
import type { components } from '@/api/schema';

type Grab = components['schemas']['dto.Grab'];

export function ScanLinkedGrabsCard({ grabs }: { grabs: readonly Grab[] }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  if (grabs.length === 0) return null;
  return (
    <Card data-testid="scan-linked-grabs-card">
      <CardHeader className="flex flex-row items-center justify-between py-3">
        <CardTitle className="text-[14px] font-semibold">
          {t('scanDetail.linkedGrabsTitle')}
        </CardTitle>
        <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>
          {t('scanDetail.linkedGrabsAllLink')}
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('scanDetail.linkedColRelease')}</TableHead>
              <TableHead>{t('scanDetail.linkedColStatus')}</TableHead>
              <TableHead>{t('scanDetail.linkedColIndexer')}</TableHead>
              <TableHead>{t('scanDetail.linkedColUpdated')}</TableHead>
              <TableHead>{t('scanDetail.linkedColAttempts')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {grabs.map((g) => (
              <TableRow
                key={g.id}
                tabIndex={0}
                role="button"
                data-testid="scan-linked-grabs-row"
                onClick={() => g.id && navigate(`/grabs?drawer=${encodeURIComponent(g.id)}`)}
                aria-label={t('scanDetail.openGrabAria', { title: g.release_title ?? g.id ?? '' })}
                className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
              >
                <TableCell className="font-mono text-[12px] max-w-md truncate">
                  {g.release_title ?? '—'}
                </TableCell>
                <TableCell><StatusBadge value={g.status} /></TableCell>
                <TableCell className="font-mono text-muted">{g.indexer_name ?? '—'}</TableCell>
                <TableCell className="text-muted">{relativeTime(g.updated_at ?? g.created_at)}</TableCell>
                <TableCell className="font-mono">{g.attempts ?? 0}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
