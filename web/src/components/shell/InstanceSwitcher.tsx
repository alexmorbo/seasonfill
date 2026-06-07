import { useTranslation } from "react-i18next"
import { ChevronsUpDown } from "lucide-react"

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useInstances, type Instance } from "@/lib/instances"
import { useInstanceFilter } from "@/lib/instance-filter-context-internal"
import { healthKind, KIND_DOT } from "@/lib/badge-variants"
import { cn } from "@/lib/utils"

function dotClass(inst: Instance | undefined): string {
  if (!inst) return KIND_DOT.neutral
  return KIND_DOT[healthKind(inst.health)]
}

export function InstanceSwitcher() {
  const { t } = useTranslation()
  const { data } = useInstances()
  const { filter, setFilter } = useInstanceFilter()
  const list = data?.instances ?? []
  const active =
    list.find((i) => i.name === filter) ?? list[0]
  const mode = active?.mode ?? "auto"
  const metaKey =
    mode === "manual"
      ? "shell.instanceSwitch.metaManual"
      : "shell.instanceSwitch.metaAuto"

  return (
    <div className="mx-3 mb-3">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className={cn(
              "w-full flex items-center gap-2.5 rounded-md border border-border-subtle",
              "bg-bg-surface px-2.5 py-1.5 text-left",
              "hover:bg-bg-surface-2 hover:border-border-strong transition-colors",
            )}
          >
            <span
              className={cn(
                "inline-block w-[7px] h-[7px] rounded-full shrink-0",
                dotClass(active),
              )}
            />
            <span className="flex-1 min-w-0">
              <span className="block text-[13.5px] font-semibold leading-tight truncate">
                {active?.name ?? t("shell.instanceSwitch.none")}
              </span>
              <span className="block text-[11px] text-tx-faint truncate">
                {active ? t(metaKey, { mode }) : ""}
              </span>
            </span>
            <ChevronsUpDown className="w-3.5 h-3.5 text-tx-muted shrink-0" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-[220px]">
          {list.length === 0 && (
            <DropdownMenuItem disabled>
              {t("shell.instanceSwitch.none")}
            </DropdownMenuItem>
          )}
          {list.map((inst) => {
            const name = inst.name ?? ""
            if (!name) return null
            return (
              <DropdownMenuItem
                key={name}
                onSelect={() => setFilter(name)}
                className="flex items-center gap-2"
              >
                <span
                  className={cn(
                    "inline-block w-1.5 h-1.5 rounded-full",
                    dotClass(inst),
                  )}
                />
                <span className="truncate">{name}</span>
                <span className="ml-auto text-[10.5px] text-tx-faint">
                  {inst.mode ?? "auto"}
                </span>
              </DropdownMenuItem>
            )
          })}
          {filter && (
            <DropdownMenuItem onSelect={() => setFilter(null)}>
              {t("shell.instanceSwitch.clear")}
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
