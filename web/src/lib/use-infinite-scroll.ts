import { useEffect, useRef } from 'react';

export interface UseInfiniteScrollArgs {
  readonly hasNextPage: boolean | undefined;
  readonly isFetchingNextPage: boolean;
  readonly fetchNextPage: () => unknown;
  // Override the rootMargin in tests or for tighter / looser pre-load
  // thresholds. Default '200px' fires fetch ~one viewport-edge before
  // the sentinel actually enters the viewport.
  readonly rootMargin?: string;
}

export interface UseInfiniteScrollResult {
  readonly sentinelRef: React.RefObject<HTMLDivElement | null>;
}

/**
 * Observe a sentinel element; trigger fetchNextPage() when it scrolls
 * into view, provided more pages exist and we are not already mid-flight.
 *
 * Caller is responsible for rendering <div ref={sentinelRef} /> below
 * (or near) the list. The element must occupy at least 1 px of vertical
 * space; an empty <div className="h-1" /> works.
 */
export function useInfiniteScroll({
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  rootMargin = '200px',
}: UseInfiniteScrollArgs): UseInfiniteScrollResult {
  const sentinelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    if (typeof IntersectionObserver === 'undefined') return;
    if (!hasNextPage) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry?.isIntersecting) return;
        if (isFetchingNextPage) return;
        if (!hasNextPage) return;
        fetchNextPage();
      },
      { rootMargin, threshold: 0 },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, rootMargin]);

  return { sentinelRef };
}
