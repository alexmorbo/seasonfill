import { describe, expect, it, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

const navigateSpy = vi.fn();
vi.mock('react-router-dom', () => ({
  useNavigate: () => navigateSpy,
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { error: (m: string) => toastError(m) },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k: string) => k }),
}));

const apiSpy = vi.fn();
vi.mock('@/lib/api', () => ({
  api: (path: string) => apiSpy(path),
}));

import { useResolveSeriesNav } from './useResolveSeriesNav';

beforeEach(() => {
  navigateSpy.mockClear();
  toastError.mockClear();
  apiSpy.mockReset();
});

describe('useResolveSeriesNav()', () => {
  it('navigates directly when a canonical seriesId is provided', async () => {
    const { result } = renderHook(() => useResolveSeriesNav());
    await result.current.resolveAndNavigate({ seriesId: 42 });
    expect(navigateSpy).toHaveBeenCalledWith('/series/42');
    expect(apiSpy).not.toHaveBeenCalled();
  });

  it('resolves a tmdbId then navigates to the canonical route', async () => {
    apiSpy.mockResolvedValue({ series_id: 7 });
    const { result } = renderHook(() => useResolveSeriesNav());
    await result.current.resolveAndNavigate({ tmdbId: 1399 });
    expect(navigateSpy).toHaveBeenCalledWith('/series/7');
  });

  it('shows an error toast and does not navigate when resolve rejects', async () => {
    apiSpy.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useResolveSeriesNav());
    await result.current.resolveAndNavigate({ tmdbId: 1399 });
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('discovery.error.resolve_failed'),
    );
    expect(navigateSpy).not.toHaveBeenCalled();
  });
});
