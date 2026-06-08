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

// reason: provider component + its consumer hooks are the public API of
// this module. Co-located is the idiomatic React-context pattern.
// eslint-disable-next-line react-refresh/only-export-components
export function usePageTitle(): { title: string; setTitle: (t: string) => void } {
  const { title, setTitle } = useCtx()
  return { title, setTitle }
}

// eslint-disable-next-line react-refresh/only-export-components
export function usePageActions(): {
  actions: React.ReactNode | null
  setActions: (a: React.ReactNode | null) => void
} {
  const { actions, setActions } = useCtx()
  return { actions, setActions }
}

// eslint-disable-next-line react-refresh/only-export-components
export function useSetPageTitle(next: string): void {
  const { setTitle } = useCtx()
  React.useEffect(() => {
    setTitle(next)
  }, [next, setTitle])
}
