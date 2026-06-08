// buildPosterUrl returns the absolute API path for the Story 080
// poster proxy endpoint. Centralised so tests and components share one
// source of truth. The path is same-origin so the existing cookie /
// X-Api-Key dispatch picks it up automatically.
export type PosterSize = 'full' | 'small';

export function buildPosterUrl(
  instance: string,
  seriesId: number,
  size: PosterSize = 'full',
): string {
  return `/api/v1/instances/${encodeURIComponent(instance)}/series/${seriesId}/poster?size=${size}`;
}
