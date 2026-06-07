import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { ScanHeaderCard } from '../ScanHeaderCard';
import type { Scan } from '@/lib/scans';

const qc = new QueryClient();
const wrap = (n: React.ReactElement) => (
  <I18nextProvider i18n={i18n}>
    <QueryClientProvider client={qc}>
      <MemoryRouter>{n}</MemoryRouter>
    </QueryClientProvider>
  </I18nextProvider>
);

const base: Scan = {
  id: 'abcd1234-0000-0000-0000-000000000001',
  instance: 'alpha', trigger: 'cron', status: 'completed',
  started_at: new Date(Date.now() - 60_000).toISOString(),
  finished_at: new Date().toISOString(),
  series_scanned: 150, candidates_found: 47, grabs_performed: 12, grabs_failed: 0,
  dry_run: false, errors_count: 0,
} as Scan;

describe('<ScanHeaderCard />', () => {
  it('renders 5 plain chips + 1 accent grabs chip = 6 total', () => {
    render(wrap(<ScanHeaderCard scan={base} />));
    expect(screen.getAllByTestId(/scan-chip/)).toHaveLength(6);
    expect(screen.getByTestId('scan-chip-accent')).toBeInTheDocument();
  });

  it('shows the copy-to-clipboard affordance', () => {
    render(wrap(<ScanHeaderCard scan={base} />));
    expect(screen.getByTestId('scan-header-copy')).toBeInTheDocument();
  });

  it('mounts ScanProgressBar + poll indicator when status=running', () => {
    const { finished_at: _omit, ...rest } = base;
    void _omit;
    render(wrap(<ScanHeaderCard scan={{ ...rest, status: 'running' } as Scan} />));
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
    expect(screen.getByTestId('poll-indicator')).toBeInTheDocument();
  });

  it('shows grabs_failed suffix when grabs_failed > 0', () => {
    render(wrap(<ScanHeaderCard scan={{ ...base, grabs_performed: 10, grabs_failed: 2 } as Scan} />));
    // 10 - 2 = 8 grabs ok; suffix appears inside the accent chip.
    expect(screen.getByTestId('scan-chip-accent').textContent).toMatch(/8/);
  });
});
