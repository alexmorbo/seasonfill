import { describe, it, expect } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LegacySeriesRedirect } from './LegacySeriesRedirect';

describe('<LegacySeriesRedirect />', () => {
  it('redirects /series/:instance/:id to /series/:id', async () => {
    render(
      <MemoryRouter initialEntries={['/series/homelab/42']}>
        <Routes>
          <Route path="/series/:instance/:id" element={<LegacySeriesRedirect />} />
          <Route
            path="/series/:id"
            element={<div data-testid="dest-detail">detail</div>}
          />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('dest-detail')).toBeInTheDocument();
    });
  });

  it('redirects /series/:instance/:id/cast to /series/:id/cast', async () => {
    render(
      <MemoryRouter initialEntries={['/series/homelab/42/cast']}>
        <Routes>
          <Route
            path="/series/:instance/:id/cast"
            element={<LegacySeriesRedirect kind="cast" />}
          />
          <Route
            path="/series/:id/cast"
            element={<div data-testid="dest-cast">cast</div>}
          />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('dest-cast')).toBeInTheDocument();
    });
  });

  it('encodes the id segment when redirecting', async () => {
    render(
      <MemoryRouter initialEntries={['/series/homelab/42%20space']}>
        <Routes>
          <Route path="/series/:instance/:id" element={<LegacySeriesRedirect />} />
          <Route
            path="/series/:id"
            element={<div data-testid="dest-detail">detail</div>}
          />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('dest-detail')).toBeInTheDocument();
    });
  });

  it('falls back to /series when id is missing', async () => {
    render(
      <MemoryRouter initialEntries={['/series/homelab/']}>
        <Routes>
          <Route path="/series/:instance/*" element={<LegacySeriesRedirect />} />
          <Route path="/series" element={<div data-testid="dest-list">list</div>} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('dest-list')).toBeInTheDocument();
    });
  });
});
