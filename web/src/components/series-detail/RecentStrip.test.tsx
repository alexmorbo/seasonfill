import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { RecentStrip } from './RecentStrip';

function withI18n(ui: React.ReactElement) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

describe('RecentStrip', () => {
  it('returns null when there are no events', () => {
    const { container } = render(withI18n(<RecentStrip recent={[]} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders up to three events with type-specific dot colors', () => {
    render(withI18n(<RecentStrip recent={[
      { event_type: 'imported', subject: 'S05E02', at: new Date(Date.now() - 2 * 3600_000).toISOString() },
      { event_type: 'grabbed',  subject: 'S05E03', at: new Date(Date.now() - 4 * 3600_000).toISOString() },
      { event_type: 'failed',   subject: 'S05E01', at: new Date(Date.now() - 86400_000).toISOString() },
      { event_type: 'imported', subject: 'S04E10', at: new Date().toISOString() },
    ]} />));
    const items = screen.getAllByTestId('recent-strip-event');
    expect(items).toHaveLength(3);
    expect(items.map(i => i.dataset['eventType'])).toEqual(['imported', 'grabbed', 'failed']);
  });
});
