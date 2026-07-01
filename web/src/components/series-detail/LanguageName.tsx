import { useTranslation } from 'react-i18next';

export interface LanguageNameProps {
  /** BCP-47 / ISO 639-1 code. Empty → returns null. */
  readonly code: string | undefined;
}

/**
 * Localized language-name renderer. Uses `Intl.DisplayNames` with the
 * resolved i18n locale; falls back to the raw code when the browser
 * doesn't recognise the language (or the API throws — rare).
 */
export function LanguageName({ code }: LanguageNameProps) {
  const { i18n } = useTranslation();
  if (!code) return null;
  let name = code;
  try {
    const locale = i18n.resolvedLanguage || 'en-US';
    const dn = new Intl.DisplayNames([locale], { type: 'language' });
    name = dn.of(code) ?? code;
  } catch {
    // Fallback to raw code on error
  }
  return <>{name}</>;
}
