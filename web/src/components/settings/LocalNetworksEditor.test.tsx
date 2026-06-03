import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { LocalNetworksEditor, LOCAL_NETWORK_DEFAULTS } from './LocalNetworksEditor';

describe('<LocalNetworksEditor />', () => {
  it('renders existing entries as chips', () => {
    render(<LocalNetworksEditor value={['10.0.0.0/8', '127.0.0.1']} onChange={() => {}} />);
    expect(screen.getByText('10.0.0.0/8')).toBeVisible();
    expect(screen.getByText('127.0.0.1')).toBeVisible();
  });

  it('shows empty-state hint when value is empty', () => {
    render(<LocalNetworksEditor value={[]} onChange={() => {}} />);
    expect(screen.getByText(/no local networks defined/i)).toBeVisible();
  });

  it('adds a valid CIDR on Enter', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={[]} onChange={onChange} id="ln" />);
    const input = screen.getByPlaceholderText(/192\.168\.1\.0\/24/i);
    await userEvent.type(input, '192.168.1.0/24{Enter}');
    expect(onChange).toHaveBeenCalledWith(['192.168.1.0/24']);
  });

  it('rejects invalid CIDR inline (no onChange call)', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={[]} onChange={onChange} />);
    const input = screen.getByPlaceholderText(/192\.168\.1\.0\/24/i);
    await userEvent.type(input, 'not-an-ip{Enter}');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(/not a valid IP or CIDR/);
  });

  it('rejects duplicates', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={['10.0.0.0/8']} onChange={onChange} />);
    const input = screen.getByPlaceholderText(/192\.168\.1\.0\/24/i);
    await userEvent.type(input, '10.0.0.0/8{Enter}');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(/already in the list/i);
  });

  it('removes via the X button', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={['10.0.0.0/8', '127.0.0.1']} onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /remove 10\.0\.0\.0\/8/i }));
    expect(onChange).toHaveBeenCalledWith(['127.0.0.1']);
  });

  it('reset-to-defaults emits the backend default set', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={['1.2.3.4/32']} onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /reset to defaults/i }));
    expect(onChange).toHaveBeenCalledWith([...LOCAL_NETWORK_DEFAULTS]);
  });

  it('Add with empty input is a no-op', async () => {
    const onChange = vi.fn();
    render(<LocalNetworksEditor value={[]} onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /^add$/i }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
