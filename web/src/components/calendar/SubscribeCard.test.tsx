import { describe, it, expect, vi, beforeEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { renderWithProviders } from '@/test-utils';
import { SubscribeCard } from './SubscribeCard';

const mintICSToken = vi.fn();
const revokeICSTokens = vi.fn();
vi.mock('@/api/calendar', () => ({
  mintICSToken: (...a: unknown[]) => mintICSToken(...a),
  revokeICSTokens: (...a: unknown[]) => revokeICSTokens(...a),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { success: (m: string) => toastSuccess(m), error: (m: string) => toastError(m) },
}));

const ICS_URL = 'https://sf.arr.morbo.dev/api/v1/calendar.ics?token=abc123';
const WEBCAL_URL = 'webcal://sf.arr.morbo.dev/api/v1/calendar.ics?token=abc123';

function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

describe('<SubscribeCard />', () => {
  beforeEach(() => {
    mintICSToken.mockReset();
    revokeICSTokens.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it('subscribe click mints a token and reveals the .ics URL', async () => {
    mintICSToken.mockResolvedValueOnce({ ics_url: ICS_URL, webcal_url: WEBCAL_URL, scope: 'all' });
    renderWithProviders(<SubscribeCard />);

    expect(screen.queryByTestId('ics-url-input')).not.toBeInTheDocument();
    await userEvent.click(screen.getByTestId('ics-subscribe-btn'));

    const input = await screen.findByTestId('ics-url-input');
    expect(input).toHaveValue(ICS_URL);
    expect(mintICSToken).toHaveBeenCalledWith('all');
    // webcal one-click link points at the webcal:// URL
    expect(screen.getByTestId('ics-open-link')).toHaveAttribute('href', WEBCAL_URL);
  });

  it('passes the selected scope to the mint call', async () => {
    mintICSToken.mockResolvedValueOnce({
      ics_url: ICS_URL,
      webcal_url: WEBCAL_URL,
      scope: 'library',
    });
    renderWithProviders(<SubscribeCard />);

    await userEvent.selectOptions(screen.getByTestId('ics-scope-select'), 'library');
    await userEvent.click(screen.getByTestId('ics-subscribe-btn'));

    await waitFor(() => expect(mintICSToken).toHaveBeenCalledWith('library'));
  });

  it('copy click writes the .ics URL to the clipboard', async () => {
    const writeText = stubClipboard();
    mintICSToken.mockResolvedValueOnce({ ics_url: ICS_URL, webcal_url: WEBCAL_URL, scope: 'all' });
    renderWithProviders(<SubscribeCard />);

    await userEvent.click(screen.getByTestId('ics-subscribe-btn'));
    await screen.findByTestId('ics-url-input');
    await userEvent.click(screen.getByTestId('ics-copy-btn'));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(ICS_URL));
  });

  it('revoke click revokes tokens and clears the shown URL', async () => {
    mintICSToken.mockResolvedValueOnce({ ics_url: ICS_URL, webcal_url: WEBCAL_URL, scope: 'all' });
    revokeICSTokens.mockResolvedValueOnce({ epoch: 1 });
    renderWithProviders(<SubscribeCard />);

    await userEvent.click(screen.getByTestId('ics-subscribe-btn'));
    expect(await screen.findByTestId('ics-url-input')).toBeInTheDocument();

    await userEvent.click(screen.getByTestId('ics-revoke-btn'));

    await waitFor(() => expect(revokeICSTokens).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByTestId('ics-url-input')).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('shows an error toast when minting fails', async () => {
    mintICSToken.mockRejectedValueOnce(new Error('boom'));
    renderWithProviders(<SubscribeCard />);

    await userEvent.click(screen.getByTestId('ics-subscribe-btn'));

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(screen.queryByTestId('ics-url-input')).not.toBeInTheDocument();
  });
});
