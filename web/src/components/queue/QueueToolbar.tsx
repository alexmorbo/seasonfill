import { useTranslation } from 'react-i18next';
import { Search, ArrowDownWideNarrow } from 'lucide-react';
import { Input } from '@/components/ui/input';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import type { QueueSort } from '@/lib/missing';

export interface QueueToolbarProps {
  readonly q: string;
  readonly sort: QueueSort;
  readonly onQChange: (v: string) => void;
  readonly onSortChange: (v: QueueSort) => void;
}

export function QueueToolbar({
  q, sort, onQChange, onSortChange,
}: QueueToolbarProps) {
  const { t } = useTranslation();
  return (
    <div
      className="flex items-center gap-2.5 mb-3.5"
      data-testid="queue-toolbar"
    >
      <div className="flex-1 relative">
        <Search
          className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 w-[15px] h-[15px] text-muted"
          aria-hidden="true"
        />
        <Input
          value={q}
          onChange={(e) => onQChange(e.target.value)}
          placeholder={t('instanceQueue.toolbar.search')}
          className="pl-8 h-9 text-[13px] bg-surface border-border-subtle"
          aria-label={t('instanceQueue.toolbar.search')}
        />
      </div>
      <Select
        value={sort}
        onValueChange={(v) => {
          if (v === 'debt' || v === 'title' || v === 'year') {
            onSortChange(v);
          }
        }}
      >
        <SelectTrigger
          className="h-9 w-auto gap-1.5 text-[12.5px] font-semibold"
          aria-label={t('instanceQueue.toolbar.sortLabel')}
        >
          <ArrowDownWideNarrow className="w-3.5 h-3.5" aria-hidden="true" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="debt">{t('instanceQueue.toolbar.sort.debt')}</SelectItem>
          <SelectItem value="title">{t('instanceQueue.toolbar.sort.title')}</SelectItem>
          <SelectItem value="year">{t('instanceQueue.toolbar.sort.year')}</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}
