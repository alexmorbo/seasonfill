import { cn } from "@/lib/utils"

export interface SparklineProps {
  data: number[]
  peakIndex?: number
  ariaLabel?: string
  className?: string
}

const MIN_RATIO = 0.08
const MAX_RATIO = 1

export function Sparkline({
  data,
  peakIndex,
  ariaLabel,
  className,
}: SparklineProps) {
  if (data.length === 0) {
    return (
      <div
        role="img"
        aria-label={ariaLabel ?? "no data"}
        className={cn(
          "flex h-9 w-full items-end gap-0.5 rounded-md bg-bg-surface-2/40 animate-pulse",
          className,
        )}
        data-empty="true"
      />
    )
  }

  const max = Math.max(...data, 1)
  const computedPeak =
    peakIndex !== undefined
      ? peakIndex
      : data.reduce(
          (best, v, i) => (v > data[best]! ? i : best),
          0,
        )

  return (
    <div
      role="img"
      aria-label={ariaLabel ?? "sparkline"}
      className={cn("flex h-9 w-full items-end gap-0.5", className)}
    >
      {data.map((v, i) => {
        const ratio = Math.max(MIN_RATIO, Math.min(MAX_RATIO, v / max))
        const isPeak = i === computedPeak
        return (
          <span
            key={i}
            data-peak={isPeak ? "true" : "false"}
            data-value={v}
            className={cn(
              "flex-1 rounded-sm transition-colors",
              isPeak ? "bg-accent" : "bg-bg-surface-2",
            )}
            style={{ height: `${ratio * 100}%` }}
          />
        )
      })}
    </div>
  )
}
