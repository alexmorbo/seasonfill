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

export function ProtectedLayout() {
  const { t } = useTranslation()
  const [scanModalOpen, setScanModalOpen] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()

  useCmdK(() => setScanModalOpen(true))

  useEffect(() => {
    const params = new URLSearchParams(location.search)
    if (params.get("new") === "1") {
      setScanModalOpen(true)
      params.delete("new")
      const next = params.toString()
      navigate(
        { pathname: location.pathname, search: next ? `?${next}` : "" },
        { replace: true },
      )
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.pathname])

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
