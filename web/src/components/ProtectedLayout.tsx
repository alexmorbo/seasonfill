import { useEffect, useState } from "react"
import { Outlet, useLocation, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"

import { AppShell } from "./shell/AppShell"
import { PageTitleProvider } from "./shell/page-title-context"
import { NetBanner } from "./NetBanner"
import { NewScanModal } from "./NewScanModal"
import { AutoGenPasswordBanner } from "./AutoGenPasswordBanner"
import { useCmdK } from "@/lib/use-cmdk"
import { InstanceFilterProvider } from "@/lib/instance-filter-context"

function hasNewParam(search: string): boolean {
  return new URLSearchParams(search).get("new") === "1"
}

export function ProtectedLayout() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()
  // Lazy init: if first render lands on ?new=1 we open the modal immediately.
  const [scanModalOpen, setScanModalOpen] = useState<boolean>(() =>
    hasNewParam(location.search),
  )

  useCmdK(() => setScanModalOpen(true))

  // Strip ?new=1 from the URL after consumption. Pure navigation; no setState.
  useEffect(() => {
    if (!hasNewParam(location.search)) return
    const params = new URLSearchParams(location.search)
    params.delete("new")
    const next = params.toString()
    navigate(
      { pathname: location.pathname, search: next ? `?${next}` : "" },
      { replace: true },
    )
  }, [location.pathname, location.search, navigate])

  return (
    <InstanceFilterProvider>
      <PageTitleProvider defaultTitle={t("nav.dashboard")}>
        <AutoGenPasswordBanner />
        <AppShell>
          <Outlet />
        </AppShell>
        <NetBanner />
        <NewScanModal open={scanModalOpen} onOpenChange={setScanModalOpen} />
      </PageTitleProvider>
    </InstanceFilterProvider>
  )
}
