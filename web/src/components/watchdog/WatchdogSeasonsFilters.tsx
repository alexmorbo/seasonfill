import { useTranslation } from 'react-i18next';
import { Search } from 'lucide-react';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import type { WatchdogSeasonsFilters } from '@/lib/api/watchdogSeasons';

const ALL_INSTANCES = '__all__';

export interface WatchdogSeasonsFiltersProps {
  readonly filters: WatchdogSeasonsFilters;
  readonly instances: readonly string[];
  readonly onChange: (next: WatchdogSeasonsFilters) => void;
}

// Three controls in a row. Mobile collapses via flex-wrap. Switches
// are labelled by adjacent <Label> so a click on the label flips them
// (Radix Switch handles the rest).
export function WatchdogSeasonsFilters({
  filters,
  instances,
  onChange,
}: WatchdogSeasonsFiltersProps) {
  const { t } = useTranslation();

  return (
    <div
      data-testid="watchdog-seasons-filters"
      className="mb-3 flex flex-wrap items-center gap-3"
    >
      <Select
        value={filters.instance ?? ALL_INSTANCES}
        onValueChange={(v) =>
          onChange({
            ...filters,
            instance: v === ALL_INSTANCES ? null : v,
          })
        }
      >
        <SelectTrigger
          className="h-8 w-[180px] text-[12.5px]"
          aria-label={t('watchdog.table.filters.instance')}
          data-testid="watchdog-seasons-filter-instance"
        >
          <SelectValue placeholder={t('watchdog.table.filters.allInstances')} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL_INSTANCES}>
            {t('watchdog.table.filters.allInstances')}
          </SelectItem>
          {instances.map((n) => (
            <SelectItem key={n} value={n}>
              {n}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-tx-faint" />
        <Input
          type="search"
          value={filters.q}
          onChange={(e) => onChange({ ...filters, q: e.target.value })}
          placeholder={t('watchdog.table.filters.search')}
          className="h-8 w-[220px] pl-7 text-[12.5px]"
          aria-label={t('watchdog.table.filters.search')}
          data-testid="watchdog-seasons-filter-search"
        />
      </div>

      <div className="flex items-center gap-2">
        <Switch
          id="watchdog-seasons-filter-cooldown"
          checked={filters.cooldownOnly}
          onCheckedChange={(v) =>
            onChange({ ...filters, cooldownOnly: Boolean(v) })
          }
          data-testid="watchdog-seasons-filter-cooldown"
        />
        <Label
          htmlFor="watchdog-seasons-filter-cooldown"
          className="cursor-pointer text-[12.5px] text-tx-secondary"
        >
          {t('watchdog.table.filters.cooldownOnly')}
        </Label>
      </div>

      <div className="flex items-center gap-2">
        <Switch
          id="watchdog-seasons-filter-blacklisted"
          checked={filters.blacklistedOnly}
          onCheckedChange={(v) =>
            onChange({ ...filters, blacklistedOnly: Boolean(v) })
          }
          data-testid="watchdog-seasons-filter-blacklisted"
        />
        <Label
          htmlFor="watchdog-seasons-filter-blacklisted"
          className="cursor-pointer text-[12.5px] text-tx-secondary"
        >
          {t('watchdog.table.filters.blacklistedOnly')}
        </Label>
      </div>
    </div>
  );
}
