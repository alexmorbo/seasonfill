import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { DashboardFirstRunState } from './DashboardFirstRunState';
import * as stepperHook from './useStepperState';

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('<DashboardFirstRunState />', () => {
  it('renders 5 steps with status badges', () => {
    vi.spyOn(stepperHook, 'useStepperState').mockReturnValue({
      steps: [
        { id: 'sonarr', status: 'done', optional: false },
        { id: 'webhook', status: 'in_progress', optional: false },
        { id: 'tmdb', status: 'todo', optional: false },
        { id: 'omdb', status: 'todo', optional: true },
        { id: 'scan', status: 'todo', optional: false },
      ],
      allRequiredDone: false,
      isLoading: false,
    });
    renderWithProviders(<DashboardFirstRunState />);
    expect(screen.getByTestId('stepper-step-sonarr')).toHaveAttribute('data-status', 'done');
    expect(screen.getByTestId('stepper-step-webhook')).toHaveAttribute('data-status', 'in_progress');
    expect(screen.getByTestId('stepper-step-tmdb')).toHaveAttribute('data-status', 'todo');
    expect(screen.getByTestId('stepper-step-omdb')).toHaveAttribute('data-status', 'todo');
    expect(screen.getByTestId('stepper-step-omdb-optional')).toBeInTheDocument();
    expect(screen.getByTestId('stepper-step-scan')).toHaveAttribute('data-status', 'todo');
  });

  it('CTA links to /instances?add=1', () => {
    vi.spyOn(stepperHook, 'useStepperState').mockReturnValue({
      steps: [
        { id: 'sonarr', status: 'todo', optional: false },
        { id: 'webhook', status: 'todo', optional: false },
        { id: 'tmdb', status: 'todo', optional: false },
        { id: 'omdb', status: 'todo', optional: true },
        { id: 'scan', status: 'todo', optional: false },
      ],
      allRequiredDone: false,
      isLoading: false,
    });
    renderWithProviders(<DashboardFirstRunState />);
    const ctaButton = screen.getByTestId('first-run-cta-add');
    // Button asChild renders <a> directly when wrapping a Link.
    const href = ctaButton.tagName.toLowerCase() === 'a'
      ? ctaButton.getAttribute('href')
      : ctaButton.querySelector('a')?.getAttribute('href');
    expect(href).toBe('/instances?add=1');
  });

  it('renders the optional badge only on the OMDb row', () => {
    vi.spyOn(stepperHook, 'useStepperState').mockReturnValue({
      steps: [
        { id: 'sonarr', status: 'todo', optional: false },
        { id: 'webhook', status: 'todo', optional: false },
        { id: 'tmdb', status: 'todo', optional: false },
        { id: 'omdb', status: 'todo', optional: true },
        { id: 'scan', status: 'todo', optional: false },
      ],
      allRequiredDone: false,
      isLoading: false,
    });
    renderWithProviders(<DashboardFirstRunState />);
    expect(screen.getByTestId('stepper-step-omdb-optional')).toBeInTheDocument();
    expect(screen.queryByTestId('stepper-step-sonarr-optional')).toBeNull();
    expect(screen.queryByTestId('stepper-step-tmdb-optional')).toBeNull();
    expect(screen.queryByTestId('stepper-step-scan-optional')).toBeNull();
  });
});
