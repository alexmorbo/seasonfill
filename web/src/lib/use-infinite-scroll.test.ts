import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useInfiniteScroll } from './use-infinite-scroll';

type IOCallback = (entries: IntersectionObserverEntry[]) => void;

interface MockObserver {
  observe: ReturnType<typeof vi.fn>;
  unobserve: ReturnType<typeof vi.fn>;
  disconnect: ReturnType<typeof vi.fn>;
  takeRecords: ReturnType<typeof vi.fn>;
  trigger: (isIntersecting: boolean) => void;
}

const instances: MockObserver[] = [];

class MockIntersectionObserver implements MockObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  takeRecords = vi.fn(() => []);
  root: Element | Document | null = null;
  rootMargin = '';
  thresholds: readonly number[] = [];
  private cb: IOCallback;
  constructor(cb: IOCallback) {
    this.cb = cb;
    instances.push(this);
  }
  trigger(isIntersecting: boolean): void {
    this.cb(
      [{ isIntersecting } as IntersectionObserverEntry],
    );
  }
}

beforeEach(() => {
  instances.length = 0;
  // Cast through unknown for the type clash between our minimal mock
  // and the full DOM interface — we only exercise the four methods.
  (globalThis as unknown as { IntersectionObserver: unknown }).IntersectionObserver =
    MockIntersectionObserver;
});
afterEach(() => {
  delete (globalThis as unknown as { IntersectionObserver?: unknown }).IntersectionObserver;
});

function attachRef(result: { current: { sentinelRef: React.RefObject<HTMLDivElement | null> } }) {
  // jsdom: simulate the sentinel mounting. We assign a real div so
  // observer.observe() has a target. Without this the hook's effect
  // bails on `!el`.
  const div = document.createElement('div');
  result.current.sentinelRef.current = div;
}

describe('useInfiniteScroll', () => {
  it('does nothing when hasNextPage is false', () => {
    const fetchNextPage = vi.fn();
    // Start with hasNextPage=true so the initial effect runs (and bails
    // on !el). After attaching the ref, flip to false: deps change →
    // effect re-runs with the ref attached, and the !hasNextPage guard
    // is the only thing standing between us and observer creation.
    // Without flipping a dep, rerender({ has: false }) is a no-op and
    // the assertion would pass even with the guard removed.
    const { result, rerender } = renderHook(
      ({ has }: { has: boolean }) =>
        useInfiniteScroll({ hasNextPage: has, isFetchingNextPage: false, fetchNextPage }),
      { initialProps: { has: true } },
    );
    attachRef(result);
    rerender({ has: false });
    expect(instances).toHaveLength(0);
    expect(fetchNextPage).not.toHaveBeenCalled();
  });

  it('creates an observer and calls fetchNextPage on intersection', () => {
    const fetchNextPage = vi.fn();
    const { result, rerender } = renderHook(
      ({ has }: { has: boolean }) =>
        useInfiniteScroll({ hasNextPage: has, isFetchingNextPage: false, fetchNextPage }),
      { initialProps: { has: false } },
    );
    // Mount sentinel + flip hasNextPage to true so the effect creates an observer.
    attachRef(result);
    rerender({ has: true });
    expect(instances).toHaveLength(1);
    expect(instances[0]!.observe).toHaveBeenCalledTimes(1);

    act(() => instances[0]!.trigger(true));
    expect(fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it('does not call fetchNextPage when already fetching', () => {
    const fetchNextPage = vi.fn();
    const { result, rerender } = renderHook(
      ({ fetching }: { fetching: boolean }) =>
        useInfiniteScroll({
          hasNextPage: true,
          isFetchingNextPage: fetching,
          fetchNextPage,
        }),
      { initialProps: { fetching: false } },
    );
    attachRef(result);
    rerender({ fetching: true });
    // Latest observer instance (effect re-ran with new fetching flag).
    const latest = instances[instances.length - 1]!;
    act(() => latest.trigger(true));
    expect(fetchNextPage).not.toHaveBeenCalled();
  });

  it('disconnects the observer on unmount', () => {
    const fetchNextPage = vi.fn();
    const { result, rerender, unmount } = renderHook(
      ({ has }: { has: boolean }) =>
        useInfiniteScroll({ hasNextPage: has, isFetchingNextPage: false, fetchNextPage }),
      { initialProps: { has: false } },
    );
    attachRef(result);
    // Flip hasNextPage so the effect re-runs with the attached ref and
    // actually creates an observer; otherwise the unmount cleanup would
    // be a no-op and this test would pass vacuously.
    rerender({ has: true });
    expect(instances).toHaveLength(1);
    unmount();
    expect(instances[0]!.disconnect).toHaveBeenCalled();
  });

  it('bails gracefully when IntersectionObserver is unavailable', () => {
    delete (globalThis as unknown as { IntersectionObserver?: unknown }).IntersectionObserver;
    const fetchNextPage = vi.fn();
    const { result, rerender } = renderHook(
      ({ has }: { has: boolean }) =>
        useInfiniteScroll({ hasNextPage: has, isFetchingNextPage: false, fetchNextPage }),
      { initialProps: { has: false } },
    );
    attachRef(result);
    // Without this rerender the effect bails on `!el` first and we'd
    // never reach the `typeof IntersectionObserver === 'undefined'`
    // guard — removing it from the hook would still leave the test
    // green. Re-running the effect with the ref attached forces the
    // guard to be the thing under test.
    rerender({ has: true });
    expect(instances).toHaveLength(0);
    expect(fetchNextPage).not.toHaveBeenCalled();
  });
});
