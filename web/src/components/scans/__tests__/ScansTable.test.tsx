import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { ScansTable } from '../ScansTable';
import type { Scan } from '@/lib/scans';

const wrap = (node: React.ReactElement) => (
  <I18nextProvider i18n={i18n}><MemoryRouter>{node}</MemoryRouter></I18nextProvider>
);

const baseScan: Scan = {
  id: 'abc12345-0000-0000-0000-000000000001',
  instance: 'alpha', trigger: 'cron', status: 'completed',
  started_at: new Date(Date.now() - 60_000).toISOString(),
  finished_at: new Date().toISOString(),
  series_scanned: 12, candidates_found: 4, grabs_performed: 2,
} as Scan;

describe('<ScansTable />', () => {
  it('renders one row per scan with chevron affordance', () => {
    render(wrap(<ScansTable rows={[baseScan, { ...baseScan, id: 'def67890-0000-0000-0000-000000000002' }]} />));
    expect(screen.getAllByTestId('scans-row')).toHaveLength(2);
  });

  it.each([
    ['completed', 'ok'],
    ['failed',    'fail'],
    ['aborted',   'abort'],
    ['running',   'running'],
  ])('st-pill variant for status=%s is %s', (status, kind) => {
    render(wrap(<ScansTable rows={[{ ...baseScan, status } as Scan]} />));
    const row = screen.getByTestId('scans-row');
    expect(row.querySelector(`[data-status-kind="${kind}"]`)).toBeTruthy();
  });
});
