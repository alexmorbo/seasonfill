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
        className={cn(
          'flex items-center gap-2.5 px-[15px] py-[13px] cursor-pointer',
          'hover:no-underline data-[state=open]:[&_svg.chev]:rotate-180',
        )}
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
