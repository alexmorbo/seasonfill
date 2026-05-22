import { Skeleton } from '@/components/ui/skeleton';
import { TableCell, TableRow } from '@/components/ui/table';

const WIDTHS = {
  xs: 'w-8',
  sm: 'w-16',
  md: 'w-24',
  lg: 'w-40',
  xl: 'w-56',
  '2xl': 'w-72',
} as const;

export type ColWidth = keyof typeof WIDTHS;

export function SkeletonRows({ rows = 5, cols }: { rows?: number; cols: readonly ColWidth[] }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, i) => (
        <TableRow key={i} className="hover:bg-transparent">
          {cols.map((w, j) => (
            <TableCell key={j}>
              <Skeleton className={`h-3.5 ${WIDTHS[w] ?? 'w-24'}`} />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}
