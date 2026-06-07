import * as React from "react"

interface PageTitleCtx {
  title: string
  setTitle: (t: string) => void
  actions: React.ReactNode | null
  setActions: (a: React.ReactNode | null) => void
}

const Ctx = React.createContext<PageTitleCtx | null>(null)

export function PageTitleProvider({
  defaultTitle,
  children,
}: {
  defaultTitle: string
  children: React.ReactNode
}) {
  const [title, setTitle] = React.useState(defaultTitle)
  const [actions, setActions] = React.useState<React.ReactNode | null>(null)
  const value = React.useMemo(
    () => ({ title, setTitle, actions, setActions }),
    [title, actions],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

function useCtx(): PageTitleCtx {
  const v = React.useContext(Ctx)
  if (!v) throw new Error("usePageTitle requires <PageTitleProvider>")
  return v
}

export function usePageTitle(): { title: string; setTitle: (t: string) => void } {
  const { title, setTitle } = useCtx()
  return { title, setTitle }
}

export function usePageActions(): {
  actions: React.ReactNode | null
  setActions: (a: React.ReactNode | null) => void
} {
  const { actions, setActions } = useCtx()
  return { actions, setActions }
}

export function useSetPageTitle(next: string): void {
  const { setTitle } = useCtx()
  React.useEffect(() => {
    setTitle(next)
  }, [next, setTitle])
}
