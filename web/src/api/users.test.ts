import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  listUsers, patchUser, deleteUser, type UserItem,
} from './users';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (...args: unknown[]) => mockApi(...args) };
});

beforeEach(() => mockApi.mockReset());

const adminRow: UserItem = {
  id: 1, username: 'admin', email: 'admin@example.com', role: 'admin',
  auth_source: 'forms',
  permissions: {
    auto_approve: true, request: true, manage_requests: true,
    manage_users: true, request_4k: true,
  },
  last_login_at: '2026-08-12T12:00:00Z', created_at: '2026-01-01T00:00:00Z',
};

describe('listUsers', () => {
  it('GETs /admin/users and unwraps the items envelope', async () => {
    mockApi.mockResolvedValueOnce({ items: [adminRow] });
    const rows = await listUsers();
    expect(mockApi).toHaveBeenCalledWith('/admin/users');
    expect(rows).toEqual([adminRow]);
  });

  it('returns [] when items is absent', async () => {
    mockApi.mockResolvedValueOnce({});
    await expect(listUsers()).resolves.toEqual([]);
  });
});

describe('patchUser', () => {
  it('PATCHes /admin/users/:id with the patch body', async () => {
    mockApi.mockResolvedValueOnce({ ...adminRow, role: 'user' });
    const r = await patchUser(1, { role: 'user' });
    expect(mockApi).toHaveBeenCalledWith('/admin/users/1',
      expect.objectContaining({ method: 'PATCH', body: { role: 'user' } }));
    expect(r.role).toBe('user');
  });

  it('sends a single permission flag as the body', async () => {
    mockApi.mockResolvedValueOnce(adminRow);
    await patchUser(3, { request: false });
    expect(mockApi).toHaveBeenCalledWith('/admin/users/3',
      expect.objectContaining({ method: 'PATCH', body: { request: false } }));
  });
});

describe('deleteUser', () => {
  it('DELETEs /admin/users/:id', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await deleteUser(3);
    expect(mockApi).toHaveBeenCalledWith('/admin/users/3',
      expect.objectContaining({ method: 'DELETE' }));
  });
});
