import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Accordion } from '@/components/ui/accordion';
import { AccordionSection } from '../AccordionSection';
import { Cable } from 'lucide-react';

describe('<AccordionSection />', () => {
  const wrap = (children: React.ReactNode, open = false) => (
    <Accordion type="multiple" value={open ? ['conn'] : []}>
      {children}
    </Accordion>
  );

  it('renders icon + title + sub-label + always pill', () => {
    render(wrap(
      <AccordionSection
        value="conn"
        icon={<Cable />}
        title="Connection"
        subLabel="A · B · C"
        alwaysPill="всегда"
      >
        <div>body</div>
      </AccordionSection>,
    ));
    expect(screen.getByText('Connection')).toBeInTheDocument();
    expect(screen.getByText('A · B · C')).toBeInTheDocument();
    expect(screen.getByTestId('accordion-always-pill').textContent).toBe('всегда');
  });

  it('renders the body when value is in the open keys', () => {
    render(wrap(
      <AccordionSection value="conn" icon={<Cable />} title="Connection">
        <div data-testid="conn-body">body</div>
      </AccordionSection>,
      true,
    ));
    expect(screen.getByTestId('conn-body')).toBeInTheDocument();
  });

  it('trigger uses justify-start and groups leading content into a flex-1 span', () => {
    render(wrap(
      <AccordionSection value="conn" icon={<Cable />} title="Connection">
        <div>body</div>
      </AccordionSection>,
    ));
    const trigger = screen.getByTestId('accordion-trigger-conn');
    // Override of the base AccordionTrigger's `justify-between`.
    expect(trigger.className).toMatch(/\bjustify-start\b/);
    expect(trigger.className).not.toMatch(/\bjustify-between\b/);
    // Leading icon + title group occupies all space to the left of the
    // chevron so the title hugs the icon at the left edge.
    const head = screen.getByTestId('accordion-trigger-head-conn');
    expect(head.className).toMatch(/\bflex-1\b/);
    expect(head.className).toMatch(/\btext-left\b/);
  });
});
