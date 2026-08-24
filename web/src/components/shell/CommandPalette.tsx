import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Command } from "cmdk"
import { Search, Plus } from "lucide-react"

import { Dialog, DialogPortal, DialogOverlay, DialogTitle } from "@/components/ui/dialog"
import { cn } from "@/lib/utils"

export interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onNewScan: () => void
}

// S2.1 — palette SHELL only: an input (250ms-debounced local state, mirroring
// discovery/SearchBar) plus one Actions command. The debounced value wires to
// NOTHING yet; S2.2 will consume `debouncedQuery` for the unified-search hook.
export function CommandPalette({ open, onOpenChange, onNewScan }: CommandPaletteProps) {
  const { t } = useTranslation()

  const [query, setQuery] = useState("")
  const [debouncedQuery, setDebouncedQuery] = useState("")
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  // Reset the query whenever the palette closes so it reopens clean.
  useEffect(() => {
    if (open) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setQuery("")
    setDebouncedQuery("")
    if (timer.current) clearTimeout(timer.current)
  }, [open])

  const onQueryChange = (next: string) => {
    setQuery(next)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setDebouncedQuery(next), 250)
  }

  // Placeholder consumer — S2.2 replaces this with the search hook.
  void debouncedQuery

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          aria-label={t("shell.cmdk.title")}
          aria-describedby={undefined}
          className={cn(
            "fixed left-[50%] top-[12%] z-50 w-full max-w-[560px] translate-x-[-50%]",
            "overflow-hidden rounded-lg border border-border-subtle bg-bg-surface shadow-lg",
            "data-[state=open]:animate-in data-[state=closed]:animate-out",
            "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
            "data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
          )}
        >
          <DialogTitle className="sr-only">{t("shell.cmdk.title")}</DialogTitle>
          <Command className="flex flex-col">
            <div className="flex items-center gap-2 border-b border-border-faint px-3.5 py-3">
              <Search className="h-4 w-4 shrink-0 text-tx-muted" />
              <Command.Input
                autoFocus
                value={query}
                onValueChange={onQueryChange}
                placeholder={t("shell.cmdk.placeholder")}
                className="flex-1 bg-transparent text-[14px] text-tx-primary outline-none placeholder:text-tx-muted"
              />
              <span className="ml-auto rounded-sm border border-border-subtle bg-bg-surface-2 px-1.5 font-mono text-[11px] text-tx-muted">
                {t("shell.cmdk.hint")}
              </span>
            </div>

            <Command.List className="max-h-[360px] overflow-y-auto p-1.5">
              <Command.Empty className="px-3 py-6 text-center text-[12.5px] text-tx-muted">
                {t("shell.cmdk.emptyHint")}
              </Command.Empty>

              <Command.Group
                heading={t("shell.cmdk.actionsHeading")}
                className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-[0.06em] [&_[cmdk-group-heading]]:text-tx-muted"
              >
                <Command.Item
                  value="new-scan"
                  onSelect={onNewScan}
                  className="flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-[13.5px] text-tx-primary data-[selected=true]:bg-bg-surface-2"
                >
                  <Plus className="h-4 w-4 text-tx-muted" />
                  {t("shell.cmdk.newScan")}
                </Command.Item>
              </Command.Group>
            </Command.List>
          </Command>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
