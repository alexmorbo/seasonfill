const IPV4 = /^(?:(?:25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d?\d)$/;

// IPv6 is messy enough that we accept anything containing colons +
// only hex/colon/dot characters (last for ::ffff:1.2.3.4) and rely
// on the server for the authoritative parse. Front-end validation's
// only job is to catch obvious typos.
const IPV6 = /^[0-9a-fA-F:.]+$/;

export function isValidCIDR(raw: string): boolean {
  const v = raw.trim();
  if (v === '') return false;
  const parts = v.split('/');
  if (parts.length > 2) return false;
  const addr = parts[0] ?? '';
  const prefix = parts[1];
  if (addr === '') return false;
  const isV4 = IPV4.test(addr);
  const isV6 = !isV4 && v.includes(':') && IPV6.test(addr);
  if (!isV4 && !isV6) return false;
  if (prefix === undefined) return true;
  const n = Number(prefix);
  if (!Number.isInteger(n)) return false;
  if (isV4) return n >= 0 && n <= 32;
  return n >= 0 && n <= 128;
}
