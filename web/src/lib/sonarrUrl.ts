// Client-side title-slug derivation used as a fallback when the wire
// payload doesn't carry Sonarr's actual `titleSlug` (Decisions / Grabs
// DTOs). Sonarr's slug algorithm is "lowercase, strip non-[a-z0-9],
// collapse runs into a single dash, trim leading/trailing dashes" — we
// approximate that so the deep-link still resolves for typical titles.
// Edge cases (titles with apostrophes, leading articles Sonarr strips,
// year-disambiguated slugs) won't match exactly, but the API also
// accepts the leading title segment in most builds.
export function slugifyTitle(title: string | null | undefined): string {
  return (title ?? '')
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

// Strip exactly one trailing slash from the instance URL before joining
// with `/series/<slug>` so we never emit `//series/...`. Mirrors the
// SeriesTitleLink helper; lifted here so SonarrLink can reuse it.
export function buildSonarrSeriesHref(
  publicUrl: string,
  slug: string,
): string {
  const base = publicUrl.replace(/\/+$/, '');
  return `${base}/series/${slug}`;
}
