import { NavLink } from "react-router-dom"
import { useTranslation } from "react-i18next"
import {
  LayoutDashboard,
  Library,
  Radar,
  GitBranch,
  Download,
  Shield,
  Server,
  Settings as SettingsIcon,
  TriangleAlert,
  ChevronDown,
} from "lucide-react"
import type { ComponentType } from "react"

import { useSession } from "@/lib/auth"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { InstanceSwitcher } from "./InstanceSwitcher"

type Item = {
  to: string
  end?: boolean
  icon: ComponentType<{ className?: string }>
  key: string
}

const OVERVIEW: Item[] = [
  { to: "/", end: true, icon: LayoutDashboard, key: "dashboard" },
  { to: "/series", icon: Library, key: "series" },
]
const ACTIVITY: Item[] = [
  { to: "/scans", icon: Radar, key: "scans" },
  { to: "/decisions", icon: GitBranch, key: "decisions" },
  { to: "/grabs", icon: Download, key: "grabs" },
  { to: "/watchdog", icon: Shield, key: "watchdog" },
]
const SETUP: Item[] = [
  { to: "/instances", icon: Server, key: "instances" },
  { to: "/settings", icon: SettingsIcon, key: "settings" },
]

function NavGroup({ label, items }: { label: string; items: Item[] }) {
  const { t } = useTranslation()
  return (
    <div className="mt-3.5">
      <div className="px-2 pb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.1em] text-tx-faint">
        {label}
      </div>
      {items.map((it) => (
        <NavLink
          key={it.key}
          to={it.to}
          end={it.end ?? false}
          className={({ isActive }) =>
            cn(
              "relative flex items-center gap-2.5 px-2 py-1.5 mb-px rounded-md",
              "text-[13.5px] text-tx-secondary",
              "hover:bg-bg-surface hover:text-tx-primary",
              isActive &&
                "bg-bg-surface-2 text-tx-primary font-medium before:content-[''] before:absolute before:left-[-8px] before:top-[7px] before:bottom-[7px] before:w-[2.5px] before:rounded-sm before:bg-accent [&>svg]:text-accent",
            )
          }
        >
          <it.icon className="w-4 h-4 shrink-0 text-tx-muted" />
          <span>{t(`nav.${it.key}`)}</span>
        </NavLink>
      ))}
    </div>
  )
}

export function AppSidebar() {
  const { t } = useTranslation()
  const { data: session } = useSession()
  const username = session?.username ?? "—"
  const initial = (username[0] ?? "?").toUpperCase()

  return (
    <aside className="bg-bg-base border-r border-border-faint flex flex-col min-h-0">
      <div className="flex items-center gap-2.5 px-4 pt-4 pb-3">
        <img
          src="/favicon.svg"
          alt=""
          className="w-6 h-6 rounded-[7px] shrink-0"
          width={24}
          height={24}
        />
        <div className="min-w-0">
          <div className="text-[15px] font-semibold tracking-tight leading-tight">
            seasonfill
          </div>
          <div className="text-[10.5px] text-tx-faint truncate">
            {t("shell.brand.sub")}
          </div>
        </div>
      </div>

      <InstanceSwitcher />

      <nav className="flex-1 overflow-y-auto px-2 pb-3 min-h-0">
        <NavGroup label={t("nav.groups.overview")} items={OVERVIEW} />
        <NavGroup label={t("nav.groups.activity")} items={ACTIVITY} />
        <NavGroup label={t("nav.groups.setup")} items={SETUP} />
      </nav>

      <div className="border-t border-border-faint p-3 flex flex-col gap-2">
        <Badge
          variant="warn"
          className="w-full justify-start gap-2 py-1.5 px-2.5 text-[12px]"
        >
          <TriangleAlert className="w-3.5 h-3.5" />
          {t("shell.webhook.placeholder")}
        </Badge>
        <div className="flex items-center gap-2.5 px-0.5">
          <span className="grid place-items-center w-6 h-6 rounded-full bg-bg-surface-2 border border-border-subtle text-[11px] font-semibold text-tx-secondary">
            {initial}
          </span>
          <span className="text-[12.5px] text-tx-secondary truncate">
            {username}
          </span>
          <ChevronDown className="w-3.5 h-3.5 text-tx-faint ml-auto" />
        </div>
      </div>
    </aside>
  )
}
