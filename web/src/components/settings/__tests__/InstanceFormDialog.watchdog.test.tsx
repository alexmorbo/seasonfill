import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import type { QueryClient } from '@tanstack/react-query';
import { renderWithProviders } from '@/test-utils';
import { InstanceFormDialog } from '../InstanceFormDialog';
import {
  instanceDetailKey,
  type InstanceDetail,
  type InstanceDetailWithMeta,
} from '@/lib/instances-mutations';
import { DtoInstanceDetailMode } from '@/api/schema';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { success: toastSuccess, error: toastError },
}));

const origFetch = globalThis.fetch;
const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

beforeEach(() => {
  toastSuccess.mockClear(); toastError.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/instances', assign: vi.fn() },
  });
  globalThis.fetch = vi.fn(async () =>
    jsonResp({ code: 'QBIT_SETTINGS_NOT_FOUND' }, 404),
  ) as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

function seedDetail(qc: QueryClient, name: string, detail: InstanceDetail) {
  const entry: InstanceDetailWithMeta = {
    detail, lastModified: 'Mon, 25 May 2026 12:00:00 GMT',
  };
  qc.setQueryData(instanceDetailKey(name), entry);
}

describe('<InstanceFormDialog /> Watchdog tab visibility', () => {
  it('does NOT render the Watchdog tab in create mode', async () => {
    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    // Other tabs are present (sanity check).
    expect(await screen.findByRole('tab', { name: /connection/i })).toBeVisible();
    expect(screen.queryByRole('tab', { name: /watchdog/i })).toBeNull();
  });

  it('renders the Watchdog tab in edit mode', async () => {
    const { qc } = renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    seedDetail(qc, 'alpha', {
      name: 'alpha', url: 'http://x', mode: DtoInstanceDetailMode.auto,
    });
    expect(await screen.findByRole('tab', { name: /watchdog/i })).toBeVisible();
  });
});
