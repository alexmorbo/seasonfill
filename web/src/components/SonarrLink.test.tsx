import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { SonarrLink } from './SonarrLink';

function wrap(ui: React.ReactElement) {
  return (
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>{ui}</TooltipProvider>
    </I18nextProvider>
  );
}

describe('<SonarrLink />', () => {
  it('renders an external icon link when publicUrl + titleSlug are present', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl="https://sonarr.example.com"
        title="Severance"
        titleSlug="severance"
      />,
    ));
    const link = screen.getByTestId('sonarr-link');
    expect(link).toHaveAttribute('href', 'https://sonarr.example.com/series/severance');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(link).toHaveAttribute('data-variant', 'icon');
  });

  it('falls back to client-side slug when titleSlug is absent', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl="https://sonarr.example.com"
        title="For All Mankind"
      />,
    ));
    expect(screen.getByTestId('sonarr-link'))
      .toHaveAttribute('href', 'https://sonarr.example.com/series/for-all-mankind');
  });

  it('renders nothing when publicUrl is missing', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl={undefined}
        title="Severance"
        titleSlug="severance"
      />,
    ));
    expect(screen.queryByTestId('sonarr-link')).toBeNull();
  });

  it('renders nothing when title is empty and no slug is provided', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl="https://sonarr.example.com"
        title=""
      />,
    ));
    expect(screen.queryByTestId('sonarr-link')).toBeNull();
  });

  it('renders chip variant with the "Sonarr" label', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl="https://sonarr.example.com"
        title="Severance"
        titleSlug="severance"
        variant="chip"
      />,
    ));
    const link = screen.getByTestId('sonarr-link');
    expect(link).toHaveAttribute('data-variant', 'chip');
    expect(link).toHaveTextContent('Sonarr');
  });

  it('strips trailing slashes from the public URL', () => {
    render(wrap(
      <SonarrLink
        instance="homelab"
        publicUrl="https://sonarr.example.com/"
        title="Andor"
        titleSlug="andor"
      />,
    ));
    expect(screen.getByTestId('sonarr-link'))
      .toHaveAttribute('href', 'https://sonarr.example.com/series/andor');
  });
});
