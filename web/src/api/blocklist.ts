import {
  useMutation, useQuery, useQueryClient,
  type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Ф5-S3 blocklist client. Hand-authored (handlers not yet in schema.ts),
// mirrors the discoveryRows.ts house pattern: bare async fns + thin RQ hooks
// over the shared `api` wrapper.

export type BlocklistKind = 'tmdb' | 'keyword';

export interface AddBlocklistBody {
  kind: BlocklistKind;
  ref_id: number;
  label?: string;
}

// Rows are discriminated by `kind`. The BE enriches tmdb rows with title +
// poster_hash from the cached series projection; keyword rows carry only the
// human label.
export interface BlocklistTmdbRow {
  readonly id: number;
  readonly kind: 'tmdb';
  readonly ref_id: number;
  readonly title: string;
  readonly poster_hash?: string;
}
export interface BlocklistKeywordRow {
  readonly id: number;
  readonly kind: 'keyword';
  readonly ref_id: number;
  readonly label: string;
}
export type BlocklistRow = BlocklistTmdbRow | BlocklistKeywordRow;

// POST echoes the created row. Enrichment (title/poster for a fresh tmdb add)
// may lag one refetch, so treat those as optional on the create response — the
// only field we strictly need is `id` (for the Undo DELETE).
export interface CreatedBlocklistRow {
  readonly id: number;
  readonly kind: BlocklistKind;
  readonly ref_id: number;
  readonly label?: string;
  readonly title?: string;
  readonly poster_hash?: string;
}

export interface KeywordSuggestion {
  readonly id: number;
  readonly name: string;
}

export const blocklistKeys = {
  list: ['discovery', 'blocklist'] as const,
  keywordSearch: (q: string) => ['discovery', 'keyword-search', q] as const,
};

// --- bare client fns ----------------------------------------------------

export async function addBlocklist(
  body: AddBlocklistBody,
): Promise<CreatedBlocklistRow> {
  return api<CreatedBlocklistRow>('/discovery/blocklist', { method: 'POST', body });
}

// CONTRACT: bare array. If the BE wraps as {items}, unwrap here + fix the test.
export async function listBlocklist(): Promise<readonly BlocklistRow[]> {
  return api<readonly BlocklistRow[]>('/discovery/blocklist');
}

export async function deleteBlocklist(id: number): Promise<void> {
  await api<void>(`/discovery/blocklist/${id}`, { method: 'DELETE' });
}

export async function searchKeywords(
  q: string,
): Promise<readonly KeywordSuggestion[]> {
  const qs = new URLSearchParams({ q });
  return api<readonly KeywordSuggestion[]>(`/discovery/keyword-search?${qs.toString()}`);
}

// --- RQ hooks -----------------------------------------------------------

export function useBlocklist(): UseQueryResult<readonly BlocklistRow[], ApiError> {
  return useQuery<readonly BlocklistRow[], ApiError>({
    queryKey: blocklistKeys.list,
    queryFn: listBlocklist,
    staleTime: 60_000,
  });
}

// Disabled below 2 chars so the typeahead doesn't fire on stray keystrokes.
export function useKeywordSearch(
  q: string,
  enabled: boolean,
): UseQueryResult<readonly KeywordSuggestion[], ApiError> {
  const trimmed = q.trim();
  return useQuery<readonly KeywordSuggestion[], ApiError>({
    queryKey: blocklistKeys.keywordSearch(trimmed),
    queryFn: () => searchKeywords(trimmed),
    enabled: enabled && trimmed.length >= 2,
    staleTime: 30_000,
  });
}

export function useAddBlocklist(): UseMutationResult<
  CreatedBlocklistRow, ApiError, AddBlocklistBody
> {
  const qc = useQueryClient();
  return useMutation<CreatedBlocklistRow, ApiError, AddBlocklistBody>({
    mutationFn: addBlocklist,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: blocklistKeys.list });
    },
  });
}

// Optimistic delete: drop the row from the cached list immediately, roll back
// on error, reconcile on settle. The optimistic slice is a no-op on surfaces
// that never fetched the list (e.g. the discovery-page Undo path) — `prev` is
// undefined there, so nothing is written.
export function useDeleteBlocklist(): UseMutationResult<
  void, ApiError, number, { prev?: readonly BlocklistRow[] | undefined }
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, number, { prev?: readonly BlocklistRow[] | undefined }>({
    mutationFn: deleteBlocklist,
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: blocklistKeys.list });
      const prev = qc.getQueryData<readonly BlocklistRow[]>(blocklistKeys.list);
      if (prev) {
        qc.setQueryData<readonly BlocklistRow[]>(
          blocklistKeys.list,
          prev.filter((r) => r.id !== id),
        );
      }
      return { prev };
    },
    onError: (_e, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(blocklistKeys.list, ctx.prev);
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: blocklistKeys.list });
    },
  });
}
