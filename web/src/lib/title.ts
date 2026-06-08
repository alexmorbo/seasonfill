// Compose a series display title with a year suffix, but skip the
// suffix when the upstream title already carries one. Sonarr appends
// "(YYYY)" to series.title for disambiguation (e.g. "Time (2021)"),
// so naive `${title} (${year})` produces "Time (2021) (2021)" — see
// PRD F-P1-4 and Story 075.
//
// When the title already ends with "(YYYY)", we keep the title as-is
// even if the caller-supplied `year` mismatches: Sonarr's embedded
// year is the canonical disambiguator (TVDB premiere year), while the
// numeric `year` field is sometimes the first-air year of a different
// production. Doubling would be worse than the mismatch.
export function formatSeriesTitle(
  title: string | null | undefined,
  year?: number | null | undefined,
): string {
  const t = (title ?? '').trim();
  if (!t) return year && year > 0 ? String(year) : '';
  if (/\(\d{4}\)\s*$/.test(t)) return t;
  if (year && year > 0) return `${t} (${year})`;
  return t;
}

// True iff the title already has a "(YYYY)" disambiguator. Callers
// that render the year as a separate node (e.g. a muted span next to
// the title) use this to suppress the secondary node and avoid
// "Time (2021) 2021" double-display.
export function titleHasEmbeddedYear(title: string | null | undefined): boolean {
  const t = (title ?? '').trim();
  return /\(\d{4}\)\s*$/.test(t);
}
