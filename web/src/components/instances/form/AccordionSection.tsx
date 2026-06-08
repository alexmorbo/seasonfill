import type { ReactNode } from 'react';
import { AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion';
import { cn } from '@/lib/utils';

export interface AccordionSectionProps {
  readonly value: string;
  readonly icon: ReactNode;
  readonly title: string;
  readonly subLabel?: string; // e.g. "Behavior · Performance · Advanced"
  readonly alwaysPill?: string; // e.g. "всегда"
  readonly children: ReactNode;
}

export function AccordionSection({
  value, icon, title, subLabel, alwaysPill, children,
}: AccordionSectionProps) {
  return (
    <AccordionItem
      value={value}
      data-testid={`accordion-section-${value}`}
      className={cn(
        'border border-border-faint rounded-lg overflow-hidden bg-surface',
      )}
    >
      <AccordionTrigger
        // `justify-start` overrides the base `justify-between` from
        // <AccordionTrigger> so the icon + title group hugs the left
        // edge instead of being spaced evenly across the row. The
        // chevron stays on the right because the leading children all
        // live inside a single flex-1 span; the chevron is appended
        // OUTSIDE that span by the base trigger.
        className={cn(
          'flex items-center gap-2.5 px-[15px] py-[13px] cursor-pointer justify-start',
          'hover:no-underline data-[state=open]:[&_svg.chev]:rotate-180',
        )}
        data-testid={`accordion-trigger-${value}`}
      >
        <span
          className="flex items-center gap-2.5 flex-1 min-w-0 text-left"
          data-testid={`accordion-trigger-head-${value}`}
        >
          <span className="text-tx-muted flex w-[15px] h-[15px]">{icon}</span>
          <span className="text-[13.5px] font-semibold">{title}</span>
          {subLabel && (
            <span className="text-[11.5px] text-tx-faint ml-1.5">{subLabel}</span>
          )}
          {alwaysPill && (
            <span
              data-testid="accordion-always-pill"
              className={cn(
                'font-mono text-[10px] text-tx-faint',
                'bg-surface-2 border border-border-faint px-[7px] py-[1px] rounded-[5px]',
              )}
            >
              {alwaysPill}
            </span>
          )}
        </span>
      </AccordionTrigger>
      <AccordionContent
        className={cn(
          'flex flex-col gap-[14px] px-[15px] py-4 border-t border-border-faint',
        )}
      >
        {children}
      </AccordionContent>
    </AccordionItem>
  );
}
