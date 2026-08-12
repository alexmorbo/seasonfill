import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  listRequests, approveRequest, denyRequest, type RequestItem,
} from './requests';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (...args: unknown[]) => mockApi(...args) };
});

beforeEach(() => mockApi.mockReset());

const tvRow: RequestItem = {
  id: 7, user_id: 2, username: 'alice', media_type: 'tv', tmdb_id: 1399,
  title: 'Breaking Bad', seasons: [1, 2], status: 'pending',
  created_at: '2026-08-12T12:00:00Z',
};

describe('listRequests', () => {
  it('GETs /requests and unwraps the items envelope', async () => {
    mockApi.mockResolvedValueOnce({ items: [tvRow] });
    const rows = await listRequests();
    expect(mockApi).toHaveBeenCalledWith('/requests');
    expect(rows).toEqual([tvRow]);
    expect(rows[0]?.seasons).toEqual([1, 2]);
  });

  it('returns [] when items is absent', async () => {
    mockApi.mockResolvedValueOnce({});
    await expect(listRequests()).resolves.toEqual([]);
  });
});

describe('approveRequest', () => {
  it('POSTs /requests/:id/approve and returns the item', async () => {
    mockApi.mockResolvedValueOnce({ ...tvRow, status: 'approved', approver_id: 1 });
    const r = await approveRequest(7);
    expect(mockApi).toHaveBeenCalledWith('/requests/7/approve',
      expect.objectContaining({ method: 'POST' }));
    expect(r.status).toBe('approved');
  });
});

describe('denyRequest', () => {
  it('POSTs /requests/:id/deny and returns the item', async () => {
    mockApi.mockResolvedValueOnce({ ...tvRow, status: 'denied', approver_id: 1 });
    const r = await denyRequest(7);
    expect(mockApi).toHaveBeenCalledWith('/requests/7/deny',
      expect.objectContaining({ method: 'POST' }));
    expect(r.status).toBe('denied');
  });
});
