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
