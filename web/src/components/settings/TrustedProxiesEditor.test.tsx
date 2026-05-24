import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { isValidCIDR, TrustedProxiesEditor } from './TrustedProxiesEditor';

describe('isValidCIDR', () => {
  it.each([
    ['10.0.0.0/8', true],
    ['10.0.0.0', true],
    ['255.255.255.255/32', true],
    ['::1', true],
    ['2001:db8::/32', true],
    ['256.0.0.1', false],
    ['10.0.0.0/33', false],
    ['10.0.0.0/abc', false],
    ['10.0.0.0/8/9', false],
    ['', false],
    ['not-an-ip', false],
  ])('CIDR %s → %s', (input, expected) => {
    expect(isValidCIDR(input)).toBe(expected);
  });
});

describe('<TrustedProxiesEditor />', () => {
  it('renders existing entries as chips', () => {
    render(<TrustedProxiesEditor value={['10.0.0.0/8', '127.0.0.1']} onChange={() => {}} />);
    expect(screen.getByText('10.0.0.0/8')).toBeVisible();
    expect(screen.getByText('127.0.0.1')).toBeVisible();
  });

  it('shows the empty-state message when value is empty', () => {
    render(<TrustedProxiesEditor value={[]} onChange={() => {}} />);
    expect(screen.getByText(/no trusted proxies/i)).toBeVisible();
  });

  it('adds a valid CIDR on Enter', async () => {
    const onChange = vi.fn();
    render(<TrustedProxiesEditor value={[]} onChange={onChange} id="tp" />);
    const input = screen.getByPlaceholderText(/10\.0\.0\.0\/8/i);
    await userEvent.type(input, '192.168.1.0/24{Enter}');
    expect(onChange).toHaveBeenCalledWith(['192.168.1.0/24']);
  });

  it('rejects invalid CIDR inline (no onChange call)', async () => {
    const onChange = vi.fn();
    render(<TrustedProxiesEditor value={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText(/10\.0\.0\.0\/8/i);
    await userEvent.type(input, 'not-an-ip{Enter}');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(/not a valid IP or CIDR/);
  });

  it('rejects duplicates', async () => {
    const onChange = vi.fn();
    render(<TrustedProxiesEditor value={['10.0.0.0/8']} onChange={onChange} />);
    const input = screen.getByPlaceholderText(/10\.0\.0\.0\/8/i);
    await userEvent.type(input, '10.0.0.0/8{Enter}');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(/already in the list/);
  });

  it('removes via the X button', async () => {
    const onChange = vi.fn();
    render(<TrustedProxiesEditor value={['10.0.0.0/8', '127.0.0.1']} onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /remove 10\.0\.0\.0\/8/i }));
    expect(onChange).toHaveBeenCalledWith(['127.0.0.1']);
  });

  it('clicking Add with empty input is a no-op (no error, no change)', async () => {
    const onChange = vi.fn();
    render(<TrustedProxiesEditor value={[]} onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /add/i }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
