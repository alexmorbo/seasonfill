// Builds the qBit Web UI deep link for a torrent hash.
// Web UI route: `{base}/#/torrent/{hash_lower}`.
// Returns null when either input is empty.
//
// Examples:
//   buildQbitDeepLink('http://qbit:8080', 'C2CB0D9E…') → 'http://qbit:8080/#/torrent/c2cb0d9e…'
//   buildQbitDeepLink('http://qbit:8080/', 'AbCdEf')  → 'http://qbit:8080/#/torrent/abcdef'
//   buildQbitDeepLink(undefined, 'abcd')              → null
//   buildQbitDeepLink('http://qbit:8080', '')         → null
export function buildQbitDeepLink(
  baseUrl: string | null | undefined,
  hash: string | null | undefined,
): string | null {
  if (!baseUrl || !hash) return null;
  const trimmed = baseUrl.trim().replace(/\/+$/, '');
  if (!trimmed) return null;
  return `${trimmed}/#/torrent/${hash.toLowerCase()}`;
}

// isKubeInternalHost — heuristic for "browser cannot reach this URL".
// Treats a URL as kube-internal iff its hostname has no dot AND is not
// `localhost`. Used by GrabDrawer (083, F-P2-1) to hide the qBit link
// when only the in-cluster `qbit_url` is populated and would 404 in
// the operator's browser.
//
// Examples:
//   isKubeInternalHost('http://qbittorrent-web:10095') → true
//   isKubeInternalHost('http://qbit.example.com')      → false
//   isKubeInternalHost('http://localhost:8080')        → false
//   isKubeInternalHost('http://127.0.0.1:8080')        → false (has dots)
//   isKubeInternalHost('')                              → false
//   isKubeInternalHost('not a url')                    → false
export function isKubeInternalHost(url: string | null | undefined): boolean {
  if (!url) return false;
  let host: string;
  try {
    host = new URL(url.trim()).hostname;
  } catch {
    return false;
  }
  if (!host) return false;
  if (host === 'localhost') return false;
  return !host.includes('.');
}
