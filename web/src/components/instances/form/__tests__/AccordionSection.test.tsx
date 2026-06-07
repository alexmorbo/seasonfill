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
});
