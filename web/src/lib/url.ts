/**
 * httpURL returns the input iff it parses as an http(s) URL.
 * Bare hostnames, paths, empty strings and undefined all return
 * null. Used to gate the "open in Sonarr" link on the instance
 * hero so we never produce `https://sonarr/` from a schemeless
 * in-cluster service name.
 */
export function httpURL(raw: string | null | undefined): string | null {
  if (raw === null || raw === undefined) return null;
  const trimmed = raw.trim();
  if (trimmed === '') return null;
  const lower = trimmed.toLowerCase();
  if (!lower.startsWith('http://') && !lower.startsWith('https://')) {
    return null;
  }
  return trimmed;
}

/**
 * pickPublicHref picks the best browser-facing URL to link to.
 * Prefers the operator-configured public override; falls back to
 * the internal URL; returns null when neither is a real http(s)
 * URL (so the caller hides the link rather than producing a
 * broken navigation).
 */
export function pickPublicHref(
  publicURL: string | null | undefined,
  internalURL: string | null | undefined,
): string | null {
  return httpURL(publicURL) ?? httpURL(internalURL);
}
