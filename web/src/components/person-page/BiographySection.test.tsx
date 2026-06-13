import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { BiographySection } from './BiographySection';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<BiographySection />', () => {
  it('returns null when biography is empty', () => {
    const { container } = r(
      <BiographySection biography="" bioLanguage={undefined} uiLanguage="en" sync={undefined} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders biography text and toggles expand/collapse', () => {
    r(<BiographySection
      biography="A long biography that should be clamped initially."
      bioLanguage="en-US"
      uiLanguage="en"
      sync={undefined}
    />);
    const text = screen.getByTestId('person-bio-text');
    expect(text.className).toContain('line-clamp-4');
    fireEvent.click(screen.getByTestId('person-bio-toggle'));
    expect(text.className).not.toContain('line-clamp-4');
  });

  it('renders the EN fallback chip when bio language differs from UI', () => {
    r(<BiographySection
      biography="..."
      bioLanguage="en-US"
      uiLanguage="ru"
      sync={undefined}
    />);
    expect(screen.getByTestId('person-bio-en-chip')).toBeInTheDocument();
  });

  it('hides the EN chip when languages match', () => {
    r(<BiographySection
      biography="..."
      bioLanguage="en-US"
      uiLanguage="en-US"
      sync={undefined}
    />);
    expect(screen.queryByTestId('person-bio-en-chip')).toBeNull();
  });

  it('renders the Source line only when sync.synced_at is present', () => {
    const { rerender } = r(
      <BiographySection biography="..." bioLanguage="en" uiLanguage="en" sync={undefined} />,
    );
    expect(screen.queryByTestId('person-bio-source')).toBeNull();
    rerender(
      <I18nextProvider i18n={i18n}>
        <BiographySection
          biography="..."
          bioLanguage="en"
          uiLanguage="en"
          sync={{ source: 'tmdb_person', synced_at: new Date().toISOString() }}
        />
      </I18nextProvider>,
    );
    expect(screen.getByTestId('person-bio-source')).toBeInTheDocument();
  });
});
