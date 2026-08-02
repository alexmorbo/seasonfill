import { useRef } from 'react';

// HARDEN-1: shared tick-cap for the degraded-poll hooks. Three pollers
// (useSeriesOverview, useSeriesRecommendations, discovery grids) re-fetch
// every few seconds while the response is degraded. Without a ceiling a
// permanently-degraded item (e.g. a media-cold poster whose blob never
// downloads) polls FOREVER. This helper owns the tick counter + cap so the
// call sites mirror `useSeries`'s POLL_MAX_TICKS semantics from one place.
//
// Two cap modes:
//   'length-reset' — overview/recs, byte-identical to useSeries: the counter
//      RESETS whenever `degraded[].length` changes, so each fresh degraded
//      wave re-earns the full budget; a stuck wave stops at `maxTicks`.
//   'absolute'     — discovery cold-start: the warming flag is STABLE during
//      warm-up, so a length-reset would never fire. Instead cap TOTAL
//      consecutive degraded ticks so a legitimate long cold-start runs to a
//      larger absolute ceiling but can never poll forever.

export type PollMode = 'length-reset' | 'absolute';

export interface DegradedPollConfig<T> {
  // Master switch (e.g. pollWhileDegraded). When false, polling is off and
  // the tick counter is held reset.
  readonly enabled: boolean;
  // True while the response is still degraded on a source worth polling.
  readonly isDegraded: (data: T | undefined) => boolean;
  // Base poll interval (ms) while degraded and under the cap. May itself
  // return false to stop early (e.g. discovery healthy).
  readonly intervalFor: (data: T | undefined) => number | false;
  // Hard ceiling on degraded ticks before polling stops.
  readonly maxTicks: number;
  readonly mode: PollMode;
  // Required for 'length-reset'; ignored for 'absolute'.
  readonly degradedLen?: (data: T | undefined) => number;
}

interface TickState {
  lastLen: number;
  ticks: number;
}

// Pure core, shared by the hook (ref-held state) and the factory
// (closure-held state) so both paths cap identically. Mutates `state`
// in place and returns the next interval (or false to stop).
function step<T>(
  state: TickState,
  data: T | undefined,
  cfg: DegradedPollConfig<T>,
): number | false {
  if (!cfg.enabled || !cfg.isDegraded(data)) {
    state.lastLen = -1;
    state.ticks = 0;
    return false;
  }
  const interval = cfg.intervalFor(data);
  if (interval === false) {
    state.lastLen = -1;
    state.ticks = 0;
    return false;
  }
  if (cfg.mode === 'length-reset') {
    const len = cfg.degradedLen ? cfg.degradedLen(data) : 0;
    if (len === state.lastLen) {
      state.ticks += 1;
    } else {
      state.lastLen = len;
      state.ticks = 1;
    }
  } else {
    state.ticks += 1;
  }
  if (state.ticks > cfg.maxTicks) return false;
  return interval;
}

// Non-React factory: a closure owning its own tick counter. Call the
// returned fn once per poll tick. Used by the unit tests (and any non-hook
// caller) to exercise the exact cap semantics without React.
export function createDegradedPollInterval<T>(
  cfg: DegradedPollConfig<T>,
): (data: T | undefined) => number | false {
  const state: TickState = { lastLen: -1, ticks: 0 };
  return (data) => step(state, data, cfg);
}

// Minimal query shape the returned callback reads. React Query passes the
// full Query object; we only touch state.data.
export interface PollQueryLike<T> {
  readonly state: { readonly data: T | undefined };
}

// React hook: same cap semantics, tick counter held in a ref so it survives
// re-renders. Reads `cfg` fresh each render so toggling `enabled`
// (pollWhileDegraded) takes effect immediately. Returns the callback shape
// React Query's `refetchInterval` option expects.
export function useDegradedPollInterval<T>(
  cfg: DegradedPollConfig<T>,
): (query: PollQueryLike<T>) => number | false {
  const ref = useRef<TickState>({ lastLen: -1, ticks: 0 });
  return (query) => step(ref.current, query.state.data, cfg);
}
