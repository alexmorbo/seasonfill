import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface LanguageFallbackTagProps {
  // BCP-47 tag served by the API (e.g. "en-US" or "en").
  readonly contentLang: string | undefined;
  // The language the UI requested. Falls back to "en" when undefined.
  readonly requestedLang?: string | undefined;
  readonly className?: string | undefined;
  // Per-section testid so multiple instances on the page can be
  // queried independently (overview / cast / season / …).
  readonly testid?: string | undefined;
}

// "en-US" → "en", "ru" → "ru", "" → undefined. Lowercased so the
// comparator below is case-insensitive.
function family(tag: string | undefined): string | undefined {
  if (!tag) return undefined;
  const t = tag.toLowerCase();
  const dash = t.indexOf('-');
  return dash === -1 ? t : t.slice(0, dash);
}

export function LanguageFallbackTag({
  contentLang,
  requestedLang,
  className,
  testid,
}: LanguageFallbackTagProps) {
  const { i18n } = useTranslation();
  const wanted = family(requestedLang ?? i18n.resolvedLanguage ?? 'en');
  const got = family(contentLang);

  // Hide when content language is unknown (we can't make any claim),
  // or when the families already match (no fallback occurred).
  if (!got || !wanted || got === wanted) return null;

  return (
    <span
      data-testid={testid ?? 'language-fallback-tag'}
      data-content-lang={got}
      className={cn(
        'rounded border border-border-subtle text-tx-muted px-1.5 py-0 text-[9.5px] font-semibold uppercase',
        className,
      )}
    >
      {got}
    </span>
  );
}
