import {
  useMutation, useQuery, useQueryClient,
  type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { DiscoveryListResponse } from '@/api/discovery';

// ENUM SYNC (ADR-0017 §D-1) — mirrors internal/discovery/domain/row.go.
// Any change here MUST update that Go file + the DiscoveryRail render switch.
export type RowType =
  | 'trending' | 'popular' | 'upcoming' | 'genre' | 'network'
  | 'keyword' | 'watch_provider' | 'recently_added' | 'upcoming_releases';
export type RowSource = 'tmdb_discover' | 'library';
export type MediaType = 'tv' | 'movie';

export interface DiscoveryRow {
  readonly id?: number;                       // absent for a code-default row
  readonly row_type: RowType;
  readonly source: RowSource;
  readonly media_type: MediaType;
  readonly params: Readonly<Record<string, string>>;
  readonly position: number;
  readonly enabled: boolean;
  readonly title: string;                     // Russian, server-authored
}

export interface DiscoveryRowsResponse {
  readonly rows: readonly DiscoveryRow[];
}

export const discoveryRowsKeys = {
  config: ['discovery', 'rows'] as const,
  rowDiscover: (params: Record<string, string>, lang: string) =>
    ['discovery', 'discover-row', params, lang] as const,
};

// GET /discovery/rows — effective row set (DB order, else code-default).
export function useDiscoveryRows(): UseQueryResult<DiscoveryRowsResponse, ApiError> {
  return useQuery<DiscoveryRowsResponse, ApiError>({
    queryKey: discoveryRowsKeys.config,
    queryFn: () => api<DiscoveryRowsResponse>('/discovery/rows'),
    staleTime: 5 * 60_000, // config changes rarely
  });
}

// todayISO returns YYYY-MM-DD (UTC) for the upcoming_releases first_air_date.gte injection.
export function todayISO(): string {
  return new Date().toISOString().slice(0, 10);
}

// useRowDiscover fetches /discovery/discover passing row.params VERBATIM —
// the BE discover_handler.parse() reads DOTTED keys (first_air_date.gte),
// so we must NOT route through discovery.ts:buildDiscoverQs (which renames
// first_air_date.gte → first_air_date_gte and would break upcoming_releases).
// The default-set params (with_genres / with_networks / sort_by /
// first_air_date.gte) are the exact query-param names the handler parses.
export function useRowDiscover(
  params: Record<string, string>,
  lang: string | undefined,
  enabled: boolean,
): UseQueryResult<DiscoveryListResponse, ApiError> {
  return useQuery<DiscoveryListResponse, ApiError>({
    queryKey: discoveryRowsKeys.rowDiscover(params, lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams();
      for (const [k, v] of Object.entries(params)) {
        if (v !== '') qs.set(k, v); // dotted keys passed unchanged
      }
      if (lang) qs.set('lang', lang);
      const s = qs.toString();
      return api<DiscoveryListResponse>(`/discovery/discover${s ? `?${s}` : ''}`);
    },
    enabled,
    staleTime: 30_000,
  });
}

// --- S2 write path (ADR-0017 D-3) ---------------------------------------

// DiscoveryRowInput is the mutable PUT shape (drops the readonly + optional id
// of DiscoveryRow — the BE assigns fresh ids on replace). position is the
// slice index; the BE re-densifies anyway.
export interface DiscoveryRowInput {
  row_type: RowType;
  source: RowSource;
  media_type: MediaType;
  params: Record<string, string>;
  position: number;
  enabled: boolean;
  title: string;
}

// PUT /discovery/rows — replaces the whole config, returns the persisted set.
export async function saveDiscoveryRows(
  rows: readonly DiscoveryRowInput[],
): Promise<DiscoveryRowsResponse> {
  return api<DiscoveryRowsResponse>('/discovery/rows', {
    method: 'PUT',
    body: { rows },
  });
}

// DELETE /discovery/rows — reset to code-default (clears the table).
export async function resetDiscoveryRows(): Promise<void> {
  await api<void>('/discovery/rows', { method: 'DELETE' });
}

export function useSaveDiscoveryRows(): UseMutationResult<
  DiscoveryRowsResponse, ApiError, readonly DiscoveryRowInput[]
> {
  const qc = useQueryClient();
  return useMutation<DiscoveryRowsResponse, ApiError, readonly DiscoveryRowInput[]>({
    mutationFn: (rows) => saveDiscoveryRows(rows),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: discoveryRowsKeys.config });
    },
  });
}

export function useResetDiscoveryRows(): UseMutationResult<void, ApiError, void> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, void>({
    mutationFn: () => resetDiscoveryRows(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: discoveryRowsKeys.config });
    },
  });
}

// reorderRows moves the element at `from` to `to`, returning a new array.
// Out-of-range / no-op moves return the input unchanged. Pure — unit-tested.
export function reorderRows<T>(rows: readonly T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= rows.length || to >= rows.length) {
    return rows.slice();
  }
  const next = rows.slice();
  const [moved] = next.splice(from, 1) as [T];
  next.splice(to, 0, moved);
  return next;
}
