/**
 * Story 494 / N-1d. Onboarding stepper state aggregator.
 *
 * Aggregates four live data sources into a typed StepState[] consumed by
 * DashboardFirstRunState (and the test rig).
 *
 *  1. Sonarr instances — from `useInstances()` (`/admin/instances`).
 *  2. Webhook installed — derived from `/webhooks/status` aggregate.
 *     ANY `healthy && installed` → step done. Bootstrapping-on-any-instance
 *     (Story 488) → step in-progress.
 *  3. TMDB key configured + validated — from `listExternalServices()`.
 *  4. OMDb key configured (optional) — same source. Marked optional in the
 *     StepState; never blocks the "all required done" collapse gate.
 *  5. First scan run completed — direct `/scans?limit=1` (NOT via `useScans`)
 *     so the hook doesn't depend on `InstanceFilterCtx` — step 5 is
 *     intentionally GLOBAL per spec §494 §2.
 *
 * Status semantics:
 *   - 'done'        — green check; condition satisfied.
 *   - 'in_progress' — blue spinner; transient (bootstrap, checking, running).
 *   - 'todo'        — gray; operator action required.
 *   - 'error'       — orange; explicit failure (e.g. tmdb invalid_key).
 *
 * The shape is intentionally narrow — the consumer renders i18n strings
 * keyed by `step.id`. Adding a 6th step is a one-line append + 4 i18n
 * keys; no consumer changes.
 */
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useInstances } from '@/lib/instances';
import { listExternalServices, type ExternalServiceDTO } from '@/api/externalServices';

export type StepID = 'sonarr' | 'webhook' | 'tmdb' | 'omdb' | 'scan';
export type StepStatus = 'done' | 'in_progress' | 'todo' | 'error';

export interface StepState {
  readonly id: StepID;
  readonly status: StepStatus;
  /** True for OMDb (optional) — never gates the "all done" collapse. */
  readonly optional: boolean;
}

export interface StepperState {
  readonly steps: readonly StepState[];
  /** True when all REQUIRED steps (everything except `optional`) are done. */
  readonly allRequiredDone: boolean;
  /** True while any of the underlying queries is still pending. */
  readonly isLoading: boolean;
}

interface ScansListResponse {
  readonly items?: ReadonlyArray<{ readonly status?: string }>;
}

interface WebhookAggregateResponse {
  readonly items?: ReadonlyArray<{ readonly installed?: boolean; readonly healthy?: boolean }>;
}

const REFETCH_INTERVAL_MS = 30_000;

export function useStepperState(): StepperState {
  const instancesQ = useInstances();
  const extServicesQ = useQuery<ExternalServiceDTO[]>({
    queryKey: ['external-services'],
    queryFn: listExternalServices,
    refetchInterval: REFETCH_INTERVAL_MS,
    staleTime: 15_000,
  });
  const webhookQ = useQuery<WebhookAggregateResponse>({
    queryKey: ['webhook-status'],
    queryFn: () => api<WebhookAggregateResponse>('/webhooks/status'),
    refetchInterval: REFETCH_INTERVAL_MS,
    staleTime: 15_000,
  });
  const scansQ = useQuery<ScansListResponse>({
    queryKey: ['scans', 'stepper-first'],
    queryFn: () => api<ScansListResponse>('/scans'),
    refetchInterval: REFETCH_INTERVAL_MS,
    staleTime: 15_000,
  });

  return useMemo<StepperState>(() => {
    const isLoading =
      instancesQ.isPending || extServicesQ.isPending || webhookQ.isPending || scansQ.isPending;

    const instances = instancesQ.data?.instances ?? [];
    const services = extServicesQ.data ?? [];
    const webhookItems = webhookQ.data?.items ?? [];
    const scanItems = scansQ.data?.items ?? [];

    const tmdb = services.find((s) => s.service === 'tmdb');
    const omdb = services.find((s) => s.service === 'omdb');

    // Step 1: Sonarr connected.
    const sonarrDone = instances.length > 0;

    // Step 2: Webhook installed. healthy+installed on any → done.
    //         Bootstrapping-on-any-instance → in_progress.
    const anyBootstrapping = instances.some((i) => i.health === 'Bootstrapping');
    const anyWebhookOk = webhookItems.some((w) => Boolean(w.installed) && Boolean(w.healthy));
    let webhookStatus: StepStatus = 'todo';
    if (anyWebhookOk) webhookStatus = 'done';
    else if (anyBootstrapping) webhookStatus = 'in_progress';

    // Step 3: TMDB key configured + validated.
    let tmdbStatus: StepStatus = 'todo';
    if (tmdb) {
      if (tmdb.last_validation_status === 'invalid_key') tmdbStatus = 'error';
      else if (tmdb.last_validation_status === 'valid') tmdbStatus = 'done';
      else if (tmdb.api_key_configured) tmdbStatus = 'in_progress';
    }

    // Step 4: OMDb (optional). Mirror TMDB logic; never gates collapse.
    let omdbStatus: StepStatus = 'todo';
    if (omdb) {
      if (omdb.last_validation_status === 'invalid_key') omdbStatus = 'error';
      else if (omdb.last_validation_status === 'valid') omdbStatus = 'done';
      else if (omdb.api_key_configured) omdbStatus = 'in_progress';
    }

    // Step 5: First scan run. running → in_progress; any completed → done.
    const anyRunning = scanItems.some((s) => s.status === 'running');
    const anyCompleted = scanItems.some((s) => s.status === 'completed');
    let scanStatus: StepStatus = 'todo';
    if (anyCompleted) scanStatus = 'done';
    else if (anyRunning) scanStatus = 'in_progress';

    const steps: readonly StepState[] = [
      { id: 'sonarr', status: sonarrDone ? 'done' : 'todo', optional: false },
      { id: 'webhook', status: webhookStatus, optional: false },
      { id: 'tmdb', status: tmdbStatus, optional: false },
      { id: 'omdb', status: omdbStatus, optional: true },
      { id: 'scan', status: scanStatus, optional: false },
    ];

    const allRequiredDone = steps
      .filter((s) => !s.optional)
      .every((s) => s.status === 'done');

    return { steps, allRequiredDone, isLoading };
  }, [
    instancesQ.isPending,
    instancesQ.data,
    extServicesQ.isPending,
    extServicesQ.data,
    webhookQ.isPending,
    webhookQ.data,
    scansQ.isPending,
    scansQ.data,
  ]);
}
