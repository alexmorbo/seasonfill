import { useTranslation } from 'react-i18next';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { LIBRARY_SORT_VALUES, type LibrarySort } from '@/api/person';

export interface SortControlProps {
  readonly value: LibrarySort;
  readonly onChange: (next: LibrarySort) => void;
  readonly className?: string | undefined;
}

export function SortControl({ value, onChange, className }: SortControlProps) {
  const { t } = useTranslation();
  return (
    <div className={className} data-testid="person-sort-control">
      <Select
        value={value}
        onValueChange={(next) => {
          // Radix Select can emit '' when an item is dismissed —
          // guard against that to keep the controlled state honest.
          if (next && (LIBRARY_SORT_VALUES as readonly string[]).includes(next)) {
            onChange(next as LibrarySort);
          }
        }}
      >
        <SelectTrigger
          aria-label={t('person.sort.label')}
          className="h-8 w-[140px] text-[12.5px]"
          data-testid="person-sort-trigger"
        >
          <SelectValue placeholder={t('person.sort.label')} />
        </SelectTrigger>
        <SelectContent>
          {LIBRARY_SORT_VALUES.map((opt) => (
            <SelectItem key={opt} value={opt} data-testid={`person-sort-option-${opt}`}>
              {t(`person.sort.${opt}`)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
