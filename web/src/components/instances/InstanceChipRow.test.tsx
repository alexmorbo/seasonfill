import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceChipRow } from './InstanceChipRow';

describe('<InstanceChipRow />', () => {
  it('renders missing chip linking to queue', () => {
    renderWithProviders(
      <InstanceChipRow
        instanceName="homelab"
        missingCount={294}
        qbitSettings={{ enabled: true } as never}
        webhookStatus={{ installed: true } as never}
      />,
    );
    const link = screen.getByTestId('chip-missing').closest('a');
    expect(link).toHaveAttribute('href', '/instances/homelab/queue');
    expect(screen.getByTestId('chip-watchdog')).toHaveTextContent(/running/i);
    expect(screen.getByTestId('chip-webhook').className).toMatch(/ok/);
  });

  it('renders watchdog stopped + webhook warn when degraded', () => {
    renderWithProviders(
      <InstanceChipRow
        instanceName="alpha"
        missingCount={0}
        qbitSettings={{ enabled: false } as never}
        webhookStatus={{ installed: true, error: 'sonarr unauthorized' } as never}
      />,
    );
    expect(screen.getByTestId('chip-watchdog')).toHaveTextContent(/stopped/i);
    expect(screen.getByTestId('chip-webhook').className).toMatch(/warn/);
  });

  it('hides chips with undefined data', () => {
    renderWithProviders(
      <InstanceChipRow
        instanceName="empty"
        missingCount={undefined}
        qbitSettings={undefined}
        webhookStatus={undefined}
      />,
    );
    expect(screen.queryByTestId('chip-missing')).toBeNull();
    expect(screen.queryByTestId('chip-watchdog')).toBeNull();
    expect(screen.queryByTestId('chip-webhook')).toBeNull();
  });
});
