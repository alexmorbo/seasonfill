import { NavLink, useLocation } from "react-router-dom"
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
  Check,
  ChevronDown,
  ListChecks,
  Globe,
} from "lucide-react"
import type { ComponentType } from "react"

import { useSession } from "@/lib/auth"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { useWebhookStatusAggregate } from "@/lib/api/webhookStatus"
import { useInstanceFilter } from "@/lib/instance-filter-context-internal"
import { useInstances } from "@/lib/instances"
import { InstanceSwitcher } from "./InstanceSwitcher"

type Item = {
  to: string
  end?: boolean
  icon: ComponentType<{ className?: string }>
  key: string
  // Optional override of the active-route check. Used by /instances/:name/queue
  // where router pattern matching against a literal `to` would never trigger.
  matchPathname?: (pathname: string) => boolean
}

const OVERVIEW: Item[] = [
  { to: "/", end: true, icon: LayoutDashboard, key: "dashboard" },
  { to: "/series", icon: Library, key: "series" },
]
const SETUP: Item[] = [
  { to: "/instances", icon: Server, key: "instances" },
  { to: "/settings", end: true, icon: SettingsIcon, key: "settings" },
  { to: "/settings/external-services", icon: Globe, key: "externalServices" },
]

function useActivityItems(): Item[] {
  const { filter } = useInstanceFilter()
  const { data } = useInstances()
  const list = data?.instances ?? []
  const active = list.find((i) => i.name === filter)?.name ?? list[0]?.name
  const queueTo = active ? `/instances/${active}/queue` : "/instances"
  return [
    { to: "/scans", icon: Radar, key: "scans" },
    { to: "/decisions", icon: GitBranch, key: "decisions" },
    { to: "/grabs", icon: Download, key: "grabs" },
    {
      to: queueTo,
      icon: ListChecks,
      key: "queue",
      matchPathname: (p) => /^\/instances\/[^/]+\/queue$/.test(p),
    },
    { to: "/watchdog", icon: Shield, key: "watchdog" },
  ]
}

function NavGroup({ label, items }: { label: string; items: Item[] }) {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  return (
    <div className="mt-3.5">
      <div className="px-2 pb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.1em] text-tx-faint">
        {label}
      </div>
      {items.map((it) => {
        const customActive = it.matchPathname?.(pathname)
        return (
          <NavLink
            key={it.key}
            to={it.to}
            end={it.end ?? false}
            className={({ isActive }) => {
              const active = customActive ?? isActive
              return cn(
                "relative flex items-center gap-2.5 px-2 py-1.5 mb-px rounded-md",
                "text-[13.5px] text-tx-secondary",
                "hover:bg-bg-surface hover:text-tx-primary",
                active &&
                  "bg-bg-surface-2 text-tx-primary font-medium before:content-[''] before:absolute before:left-[-8px] before:top-[7px] before:bottom-[7px] before:w-[2.5px] before:rounded-sm before:bg-accent [&>svg]:text-accent",
              )
            }}
          >
            <it.icon className="w-4 h-4 shrink-0 text-tx-muted" />
            <span>{t(`nav.${it.key}`)}</span>
          </NavLink>
        )
      })}
    </div>
  )
}

function WebhookCountBadge() {
  const { t } = useTranslation()
  const q = useWebhookStatusAggregate()
  if (q.isPending || q.isError) return null
  const data = q.data
  if (!data || data.items.length === 0) return null
  const total = data.items.length
  const unhealthy = data.unhealthy_count
  if (unhealthy === 0) {
    return (
      <Badge
        variant="ok"
        className="w-full justify-start gap-2 py-1.5 px-2.5 text-[12px]"
        data-testid="sidebar-webhook-ok"
      >
        <Check className="w-3.5 h-3.5" />
        {t("shell.webhook.ok", { count: total })}
      </Badge>
    )
  }
  return (
    <Badge
      variant="warn"
      className="w-full justify-start gap-2 py-1.5 px-2.5 text-[12px]"
      data-testid="sidebar-webhook-warn"
    >
      <TriangleAlert className="w-3.5 h-3.5" />
      {t("shell.webhook.degraded", { count: unhealthy, total })}
    </Badge>
  )
}

export function AppSidebar() {
  const { t } = useTranslation()
  const { data: session } = useSession()
  const username = session?.username ?? "—"
  const initial = (username[0] ?? "?").toUpperCase()
  const activity = useActivityItems()

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
        <NavGroup label={t("nav.groups.activity")} items={activity} />
        <NavGroup label={t("nav.groups.setup")} items={SETUP} />
      </nav>

      <div className="border-t border-border-faint p-3 flex flex-col gap-2">
        <WebhookCountBadge />
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
