import { useTranslation } from "react-i18next"
import { Search } from "lucide-react"

import { usePageActions, usePageTitle } from "./page-title-context"
import { LanguageSwitcher } from "@/components/LanguageSwitcher"

export function AppTopBar({ onOpenPalette }: { onOpenPalette?: (() => void) | undefined }) {
  const { t } = useTranslation()
  const { title } = usePageTitle()
  const { actions } = usePageActions()

  return (
    <header className="flex items-center gap-3.5 px-6 py-3.5 border-b border-border-faint h-[52px] shrink-0 bg-bg-base">
      <h1 className="text-[18px] font-semibold tracking-tight m-0">{title}</h1>
      <div className="flex-1" />
      <button
        type="button"
        onClick={() => onOpenPalette?.()}
        aria-label={t("shell.cmdk.placeholder")}
        className="hidden md:flex items-center gap-2 bg-bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-tx-muted text-[12.5px] min-w-[200px] cursor-pointer transition-colors hover:bg-bg-surface-2"
      >
        <Search className="w-3.5 h-3.5" />
        <span className="truncate">{t("shell.cmdk.placeholder")}</span>
        <span className="ml-auto font-mono text-[11px] bg-bg-surface-2 border border-border-subtle rounded-sm px-1.5">
          {t("shell.cmdk.hint")}
        </span>
      </button>
      {/* Story 121c §G — mount the LanguageSwitcher so operators can
          flip locales from the UI instead of poking localStorage. */}
      <LanguageSwitcher />
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </header>
  )
}
