import { useTranslation } from 'react-i18next';
import { Languages } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuLabel,
} from '@/components/ui/dropdown-menu';
import { SUPPORTED_LANGS } from '@/i18n';
import { useLanguage } from '@/hooks/useLanguage';

const LABELS: Record<string, string> = {
  en: 'English',
  ru: 'Русский',
};

// LanguageSwitcher — header dropdown. N-7c wires it to useLanguage's
// dual-write so a choice persists via PATCH /api/v1/me/settings, not
// just local i18n state. useLanguage handles the optimistic cache
// update + i18n switch + localStorage + rollback-on-error sequence.
//
// Falls back to i18n.resolvedLanguage if /me hasn't resolved yet
// (e.g. very first render before the hook lands). This is by design:
// the switcher should always show *something* sensible.
export function LanguageSwitcher() {
  const { t } = useTranslation();
  const language = useLanguage();
  const current = (SUPPORTED_LANGS as readonly string[]).includes(language.current)
    ? language.current
    : 'en';

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t('nav.language')}
          className="grid place-items-center w-8 h-8 border border-border rounded-md text-foreground-2 hover:bg-surface"
        >
          <Languages className="w-4 h-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[160px]">
        <DropdownMenuLabel>{t('nav.language')}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={current}
          onValueChange={(v) => {
            if (!v) return;
            void language.setLanguage(v);
          }}
        >
          {SUPPORTED_LANGS.map((code) => (
            <DropdownMenuRadioItem key={code} value={code}>
              {LABELS[code] ?? code}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
