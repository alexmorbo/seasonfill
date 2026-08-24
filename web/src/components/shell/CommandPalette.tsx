import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Command } from "cmdk"
import { Search, Plus, Loader2 } from "lucide-react"

import { Dialog, DialogPortal, DialogOverlay, DialogTitle } from "@/components/ui/dialog"
import { cn } from "@/lib/utils"
import { useResolveSeriesNav } from "@/components/series/useResolveSeriesNav"
import {
  useUnifiedSearch,
  resolveSearchPoster,
  type SeriesHit,
  type MovieHit,
  type PersonHit,
  type SearchGroup,
} from "@/api/search"
import { SearchResultRow } from "./SearchResultRow"

export interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onNewScan: () => void
}

const TOP_N = 3
const monogram = (s: string) => (s.charAt(0) || "?").toUpperCase()

export function CommandPalette({ open, onOpenChange, onNewScan }: CommandPaletteProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { resolveAndNavigate } = useResolveSeriesNav()

  const [query, setQuery] = useState("")
  const [debouncedQuery, setDebouncedQuery] = useState("")
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

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

  const search = useUnifiedSearch(debouncedQuery)

  const close = () => onOpenChange(false)
  const selectSeries = (hit: SeriesHit) => {
    close()
    void resolveAndNavigate({ seriesId: hit.id, tmdbId: hit.tmdbId })
  }
  const selectMovie = (hit: MovieHit) => {
    close()
    navigate(`/movies/${hit.tmdbId}`)
  }
  const selectPerson = (hit: PersonHit) => {
    close()
    navigate(`/person/${hit.tmdbId}`)
  }

  const renderGroup = (scope: "library" | "catalog", group: SearchGroup) => {
    const series = group.series.slice(0, TOP_N)
    const movies = group.movies.slice(0, TOP_N)
    const people = group.people.slice(0, TOP_N)
    if (series.length + movies.length + people.length === 0) return null
    const heading =
      scope === "library" ? t("shell.cmdk.groupLibrary") : t("shell.cmdk.groupCatalog")
    const sourceLabel =
      scope === "library" ? t("shell.cmdk.groupLibrary") : t("shell.cmdk.groupCatalog")

    return (
      <Command.Group
        heading={heading}
        className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-[0.06em] [&_[cmdk-group-heading]]:text-tx-muted"
      >
        {series.map((hit) => (
          <SearchResultRow
            key={`${scope}-series-${hit.id ?? hit.tmdbId}`}
            value={`${scope}-series-${hit.id ?? hit.tmdbId}`}
            title={hit.title}
            secondary={hit.year ? String(hit.year) : undefined}
            sourceLabel={sourceLabel}
            monogram={monogram(hit.title)}
            poster={resolveSearchPoster(hit)}
            onSelect={() => selectSeries(hit)}
          />
        ))}
        {movies.map((hit) => (
          <SearchResultRow
            key={`${scope}-movie-${hit.tmdbId}`}
            value={`${scope}-movie-${hit.tmdbId}`}
            title={hit.title}
            secondary={hit.year ? String(hit.year) : undefined}
            sourceLabel={sourceLabel}
            monogram={monogram(hit.title)}
            poster={resolveSearchPoster(hit)}
            onSelect={() => selectMovie(hit)}
          />
        ))}
        {people.map((hit) => (
          <SearchResultRow
            key={`${scope}-person-${hit.tmdbId}`}
            value={`${scope}-person-${hit.tmdbId}`}
            title={hit.name}
            secondary={hit.knownFor}
            sourceLabel={sourceLabel}
            monogram={monogram(hit.name)}
            poster={resolveSearchPoster(hit)}
            onSelect={() => selectPerson(hit)}
          />
        ))}
      </Command.Group>
    )
  }

  const showEmpty =
    search.enabled && !search.libraryLoading && !search.catalogSearching && !search.hasResults
  const showHint = query.trim().length === 0

  const showAllQuery = query.trim()
  const showAll = () => {
    close()
    navigate(`/search?q=${encodeURIComponent(showAllQuery)}`)
  }

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
          <Command shouldFilter={false} className="flex flex-col">
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

              {renderGroup("library", search.library)}
              {renderGroup("catalog", search.catalog)}

              {search.catalogSearching ? (
                <div
                  data-testid="cmdk-catalog-searching"
                  className="flex items-center gap-2 px-3 py-2 text-[12px] text-tx-muted"
                >
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t("shell.cmdk.searchingCatalog")}
                </div>
              ) : null}

              {showAllQuery.length >= 1 ? (
                <Command.Item
                  value="show-all-results"
                  onSelect={showAll}
                  data-testid="cmdk-show-all"
                  className="mt-1 flex cursor-pointer items-center gap-2.5 rounded-md border-t border-border-faint px-2.5 py-2 text-[13px] font-medium text-accent data-[selected=true]:bg-bg-surface-2"
                >
                  <Search className="h-4 w-4" />
                  {t("shell.cmdk.showAll")}
                </Command.Item>
              ) : null}

              {showEmpty ? (
                <div
                  data-testid="cmdk-search-empty"
                  className="px-3 py-6 text-center text-[12.5px] text-tx-muted"
                >
                  {t("shell.cmdk.searchEmpty")}
                </div>
              ) : null}

              {showHint ? (
                <div className="px-3 py-6 text-center text-[12.5px] text-tx-muted">
                  {t("shell.cmdk.emptyHint")}
                </div>
              ) : null}
            </Command.List>
          </Command>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}
