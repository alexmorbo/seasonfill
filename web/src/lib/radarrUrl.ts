// Radarr deep-links a movie by its TMDB id at /movie/{tmdbId} (Radarr v3+).
// Strip a single trailing slash so we never emit `//movie/...`. Mirrors
// buildSonarrSeriesHref (lib/sonarrUrl.ts).
export function buildRadarrMovieHref(publicUrl: string, tmdbId: number): string {
  const base = publicUrl.replace(/\/+$/, '');
  return `${base}/movie/${tmdbId}`;
}
