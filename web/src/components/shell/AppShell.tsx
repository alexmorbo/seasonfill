import * as React from "react"

import { AppSidebar } from "./AppSidebar"
import { AppTopBar } from "./AppTopBar"

export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[244px_1fr] h-screen overflow-hidden bg-bg-base text-tx-primary">
      <AppSidebar />
      <div className="min-w-0 flex flex-col overflow-hidden bg-bg-base">
        <AppTopBar />
        <div className="flex-1 overflow-y-auto min-h-0">
          <div className="px-6 py-5 pb-10">{children}</div>
        </div>
      </div>
    </div>
  )
}
