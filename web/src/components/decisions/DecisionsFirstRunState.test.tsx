import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DecisionsFirstRunState } from './DecisionsFirstRunState';

function renderFR() {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={['/decisions']}>
        <Routes>
          <Route path="/decisions" element={<DecisionsFirstRunState />} />
          <Route path="/scans" element={<div data-testid="scans-page" />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe('DecisionsFirstRunState', () => {
  it('renders the first-run copy and start-scan button', () => {
    renderFR();
    expect(screen.getByTestId('decisions-first-run-state')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /scan|скан/i })).toBeInTheDocument();
  });

  it('start-scan navigates to /scans', async () => {
    renderFR();
    await userEvent.click(screen.getByRole('button', { name: /scan|скан/i }));
    expect(screen.getByTestId('scans-page')).toBeInTheDocument();
  });
});
