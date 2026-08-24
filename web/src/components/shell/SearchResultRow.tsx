import { Command } from "cmdk"

import { cn } from "@/lib/utils"

export interface SearchResultRowProps {
  value: string
  title: string
  secondary?: string | undefined
  sourceLabel: string
  monogram: string
  poster?: string | undefined
  onSelect: () => void
  testId?: string | undefined
}

export function SearchResultRow({
  value,
  title,
  secondary,
  sourceLabel,
  monogram,
  poster,
  onSelect,
  testId,
}: SearchResultRowProps) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      data-testid={testId ?? "search-result-row"}
      className={cn(
        "flex cursor-pointer items-center gap-3 rounded-md px-2.5 py-1.5",
        "text-tx-primary data-[selected=true]:bg-bg-surface-2",
      )}
    >
      <span className="flex h-11 w-8 shrink-0 items-center justify-center overflow-hidden rounded-sm bg-bg-surface-2 text-tx-faint">
        {poster ? (
          <img
            src={poster}
            alt=""
            aria-hidden="true"
            loading="lazy"
            decoding="async"
            data-testid="search-result-poster"
            className="h-full w-full object-cover"
          />
        ) : (
          <span
            data-testid="search-result-monogram"
            className="text-sm font-semibold"
          >
            {monogram}
          </span>
        )}
      </span>

      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-[13.5px] leading-tight">{title}</span>
        {secondary ? (
          <span className="truncate text-[11.5px] leading-tight text-tx-muted">
            {secondary}
          </span>
        ) : null}
      </span>

      <span className="ml-auto shrink-0 rounded-sm border border-border-subtle bg-bg-surface px-1.5 py-0.5 text-[10.5px] text-tx-muted">
        {sourceLabel}
      </span>
    </Command.Item>
  )
}
