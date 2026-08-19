import type { ReactNode } from 'react';

export interface MediaDetailProps {
  /** Section slots rendered by later ADR-0022 stories. Inert in S1. */
  readonly children?: ReactNode;
}

/**
 * MediaDetail is the inert scaffold of the universal media-detail FE shell
 * (ADR-0022 S1). It renders whatever children it is given and nothing else — no
 * view-model, no route, no data fetching. Later stories fill it with the shared
 * section slots (hero, text, cast, recs, media, keywords, seasons, collection);
 * SeriesDetail / MovieDetail become thin adapters over it. Zero runtime effect
 * today (nothing imports it yet).
 */
export function MediaDetail({ children }: MediaDetailProps) {
  return <div className="media-detail">{children}</div>;
}
