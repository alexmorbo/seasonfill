import { useTranslation } from 'react-i18next';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { OTHER_SORT_VALUES, type OtherSort } from '@/api/person';

export interface OtherSortControlProps {
  readonly value: OtherSort;
  readonly onChange: (next: OtherSort) => void;
  readonly className?: string | undefined;
}

export function OtherSortControl({ value, onChange, className }: OtherSortControlProps) {
  const { t } = useTranslation();
  return (
    <div className={className} data-testid="person-other-sort-control">
      <Select
        value={value}
        onValueChange={(next) => {
          // Radix Select can emit '' when an item is dismissed —
          // guard against that to keep the controlled state honest.
          if (next && (OTHER_SORT_VALUES as readonly string[]).includes(next)) {
            onChange(next as OtherSort);
          }
        }}
      >
        <SelectTrigger
          aria-label={t('person.otherSort.label')}
          className="h-8 w-[170px] text-[12.5px]"
          data-testid="person-other-sort-trigger"
        >
          <SelectValue placeholder={t('person.otherSort.label')} />
        </SelectTrigger>
        <SelectContent>
          {OTHER_SORT_VALUES.map((opt) => (
            <SelectItem
              key={opt}
              value={opt}
              data-testid={`person-other-sort-option-${opt}`}
            >
              {t(`person.otherSort.${opt}`)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
