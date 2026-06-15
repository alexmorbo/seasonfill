import { useTranslation } from 'react-i18next';

export interface CountryNameProps {
  /** ISO 3166-1 alpha-2 code. Empty → returns null. */
  readonly code: string | undefined;
}

/**
 * Localized country-name renderer. Uses `Intl.DisplayNames` with the
 * resolved i18n locale; falls back to the raw code when the browser
 * doesn't recognise the region (or the API throws — rare).
 */
export function CountryName({ code }: CountryNameProps) {
  const { i18n } = useTranslation();
  if (!code) return null;
  const upper = code.toUpperCase();
  let name = upper;
  try {
    const locale = i18n.resolvedLanguage || 'en';
    const dn = new Intl.DisplayNames([locale], { type: 'region' });
    name = dn.of(upper) ?? upper;
  } catch {
    // Fallback to raw code on error
  }
  return <>{name}</>;
}
