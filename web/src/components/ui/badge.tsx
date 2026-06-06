import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex items-center gap-1 rounded-md border px-2.5 py-0.5 text-xs font-medium transition-colors focus:outline-hidden focus:ring-2 focus:ring-ring focus:ring-offset-2",
  {
    variants: {
      variant: {
        default:
          "border-transparent bg-primary text-primary-foreground shadow-sm hover:bg-primary/80",
        secondary:
          "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/80",
        destructive:
          "border-transparent bg-destructive text-destructive-foreground shadow-sm hover:bg-destructive/80",
        outline: "text-foreground",
        ok:
          "text-ok border-ok/35 bg-ok-dim",
        warn:
          "text-warn border-warn/35 bg-warn-dim",
        danger:
          "text-danger border-danger/40 bg-danger-dim",
        accent:
          "text-accent border-accent/35 bg-accent-dim",
        info:
          "text-info border-info/35 bg-info-dim",
        neutral:
          "text-tx-secondary border-border-subtle bg-bg-surface-2",
        solid:
          "text-tx-secondary border-border-subtle bg-bg-surface-2",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {
  mono?: boolean
}

function Badge({ className, variant, mono = false, ...props }: BadgeProps) {
  return (
    <div
      className={cn(
        badgeVariants({ variant }),
        mono && "font-mono tabular-nums [font-feature-settings:'zero']",
        className,
      )}
      {...props}
    />
  )
}

export { Badge, badgeVariants }
