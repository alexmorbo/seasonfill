import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ScanProgressBar } from './ScanProgressBar';

describe('<ScanProgressBar />', () => {
  it('renders indeterminate (no total) and omits aria-valuemax/now', () => {
    render(<ScanProgressBar status="running" seriesScanned={7} startedAt={new Date().toISOString()} />);
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('data-determinate', 'false');
    expect(bar).toHaveAttribute('data-status', 'running');
    expect(bar.getAttribute('aria-valuemax')).toBeNull();
    expect(bar.getAttribute('aria-valuenow')).toBeNull();
    expect(screen.getByText(/7 series scanned/i)).toBeInTheDocument();
  });
  it('renders determinate when seriesTotal supplied (future M-011d-1)', () => {
    render(<ScanProgressBar status="running" seriesScanned={4} seriesTotal={10} />);
    const bar = screen.getByRole('progressbar');
    expect(bar).toHaveAttribute('aria-valuemax', '10');
    expect(bar).toHaveAttribute('aria-valuenow', '4');
    expect(screen.getByText(/4\/10 series scanned/i)).toBeInTheDocument();
  });
  it.each(['completed', 'failed', 'aborted'])('exposes terminal status %s on the bar', (status) => {
    render(<ScanProgressBar status={status} seriesScanned={3} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('data-status', status);
  });
  it('shows the elapsed duration in the terminal label', () => {
    const started = new Date(Date.now() - 65_000).toISOString();
    render(<ScanProgressBar status="completed" seriesScanned={12} startedAt={started} finishedAt={new Date().toISOString()} />);
    expect(screen.getByText(/12 series scanned in /i)).toBeInTheDocument();
  });
});
