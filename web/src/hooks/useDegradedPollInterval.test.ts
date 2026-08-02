import { describe, it, expect } from 'vitest';
import {
  createDegradedPollInterval,
  type DegradedPollConfig,
} from './useDegradedPollInterval';

interface Fake {
  readonly degraded: readonly string[];
}

function lengthResetConfig(
  enabled: boolean,
): DegradedPollConfig<Fake> {
  return {
    enabled,
    isDegraded: (d) => (d?.degraded.length ?? 0) > 0,
    intervalFor: () => 4_000,
    maxTicks: 6,
    mode: 'length-reset',
    degradedLen: (d) => d?.degraded.length ?? 0,
  };
}

function absoluteConfig(maxTicks: number): DegradedPollConfig<Fake> {
  return {
    enabled: true,
    isDegraded: (d) => (d?.degraded.length ?? 0) > 0,
    intervalFor: () => 5_000,
    maxTicks,
    mode: 'absolute',
  };
}

describe('createDegradedPollInterval — length-reset mode', () => {
  const hot: Fake = { degraded: ['tmdb_series'] };

  it('polls up to maxTicks then stops at a stable degraded length', () => {
    const poll = createDegradedPollInterval(lengthResetConfig(true));
    for (let i = 0; i < 6; i += 1) expect(poll(hot)).toBe(4_000);
    expect(poll(hot)).toBe(false); // 7th consecutive tick — capped
    expect(poll(hot)).toBe(false); // stays stopped
  });

  it('resets the counter when degraded length changes, resuming the poll', () => {
    const poll = createDegradedPollInterval(lengthResetConfig(true));
    for (let i = 0; i < 6; i += 1) poll(hot);
    expect(poll(hot)).toBe(false); // capped at stable length 1
    const grew: Fake = { degraded: ['tmdb_series', 'omdb'] }; // length 2
    expect(poll(grew)).toBe(4_000); // length change re-earns the budget
  });

  it('returns false when disabled regardless of degraded state', () => {
    const poll = createDegradedPollInterval(lengthResetConfig(false));
    expect(poll(hot)).toBe(false);
  });

  it('returns false and resets once the response is no longer degraded', () => {
    const poll = createDegradedPollInterval(lengthResetConfig(true));
    expect(poll(hot)).toBe(4_000);
    expect(poll({ degraded: [] })).toBe(false); // healthy — stop + reset
    expect(poll(hot)).toBe(4_000); // fresh degraded wave polls again
  });
});

describe('createDegradedPollInterval — absolute mode', () => {
  const warming: Fake = { degraded: ['discovery_warming'] };

  it('does NOT stop before the absolute cap (24 stable-length ticks)', () => {
    const poll = createDegradedPollInterval(absoluteConfig(24));
    for (let i = 0; i < 24; i += 1) expect(poll(warming)).toBe(5_000);
  });

  it('stops exactly at the absolute cap even though length never changes', () => {
    const poll = createDegradedPollInterval(absoluteConfig(24));
    for (let i = 0; i < 24; i += 1) poll(warming);
    expect(poll(warming)).toBe(false); // 25th tick — capped
    expect(poll(warming)).toBe(false);
  });
});
