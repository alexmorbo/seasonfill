// Story 522 / N-4e — verifies the plain fetch helpers hit the right
// URLs and decode the wire shape.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  getQualityProfiles,
  getRootFolders,
  refreshInstanceMetadata,
} from '../instance_metadata';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (path: string, init?: RequestInit) =>
      init === undefined ? mockApi(path) : mockApi(path, init),
  };
});

const qpPayload = {
  items: [{ id: 1, name: 'HD-1080p' }],
  refreshed_at: 'Mon, 23 Jun 2026 22:00:00 GMT',
  cache_status: 'hit',
  instance_name: 'main',
};

const rfPayload = {
  items: [{ id: 2, path: '/tv', accessible: true, free_space: 1024 }],
  refreshed_at: 'Mon, 23 Jun 2026 22:00:00 GMT',
  cache_status: 'miss',
  instance_name: 'main',
};

beforeEach(() => mockApi.mockReset());

describe('getQualityProfiles', () => {
  it('hits the namespaced endpoint and returns the payload', async () => {
    mockApi.mockResolvedValueOnce(qpPayload);
    const res = await getQualityProfiles('main');
    expect(mockApi).toHaveBeenCalledWith('/instances/main/quality-profiles');
    expect(res.items[0]?.name).toBe('HD-1080p');
  });

  it('encodes the instance name', async () => {
    mockApi.mockResolvedValueOnce(qpPayload);
    await getQualityProfiles('with space');
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/with%20space/quality-profiles',
    );
  });
});

describe('getRootFolders', () => {
  it('hits the namespaced endpoint and returns paths', async () => {
    mockApi.mockResolvedValueOnce(rfPayload);
    const res = await getRootFolders('main');
    expect(mockApi).toHaveBeenCalledWith('/instances/main/root-folders');
    expect(res.items[0]?.path).toBe('/tv');
    expect(res.items[0]?.accessible).toBe(true);
  });
});

describe('refreshInstanceMetadata', () => {
  it('POSTs the refresh endpoint and returns invalidated flag', async () => {
    mockApi.mockResolvedValueOnce({ invalidated: true });
    const res = await refreshInstanceMetadata('main');
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/main/refresh-metadata',
      { method: 'POST' },
    );
    expect(res.invalidated).toBe(true);
  });
});
