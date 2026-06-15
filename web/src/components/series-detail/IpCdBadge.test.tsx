import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Flag } from 'lucide-react';
import { IpCdBadge } from './IpCdBadge';

describe('IpCdBadge', () => {
  it('renders counter variant with digit + unit', () => {
    render(<IpCdBadge digit={4} unit="дня" />);
    const el = screen.getByTestId('ip-cd-badge');
    expect(el.dataset['variant']).toBe('counter');
    expect(el.textContent).toMatch(/4/);
    expect(el.textContent).toMatch(/дня/);
  });

  it('renders muted variant with icon', () => {
    render(<IpCdBadge icon={<Flag data-testid="badge-icon" />} />);
    const el = screen.getByTestId('ip-cd-badge');
    expect(el.dataset['variant']).toBe('muted');
    expect(screen.getByTestId('badge-icon')).toBeInTheDocument();
  });
});
