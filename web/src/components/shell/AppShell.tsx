import * as React from "react"

import { AppSidebar } from "./AppSidebar"
import { AppTopBar } from "./AppTopBar"

export function AppShell({
  children,
  onOpenPalette,
}: {
  children: React.ReactNode
  onOpenPalette?: () => void
}) {
  return (
    <div className="grid grid-cols-[244px_1fr] h-screen overflow-hidden bg-bg-base text-tx-primary">
      <AppSidebar />
      <div className="min-w-0 flex flex-col overflow-hidden bg-bg-base">
        <AppTopBar onOpenPalette={onOpenPalette} />
        <div className="flex-1 overflow-y-auto min-h-0">
          <main className="mx-auto w-full max-w-[1440px] px-6 py-5 pb-10">
            {children}
          </main>
        </div>
      </div>
    </div>
  )
}
