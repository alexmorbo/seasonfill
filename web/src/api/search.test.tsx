import { describe, expect, it, vi, beforeEach } from "vitest"
import { renderHook, waitFor, act } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import type { ReactNode } from "react"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { resolvedLanguage: "en" } }),
}))

const apiSpy = vi.fn()
vi.mock("@/lib/api", () => ({
  api: (path: string) => apiSpy(path),
}))

import {
  useUnifiedSearch,
  resolveSearchPoster,
  mapSeriesItems,
  mapCollectionItems,
  type SeriesHit,
  type PersonHit,
} from "./search"

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
}

const paths = () => apiSpy.mock.calls.map((c) => c[0] as string)
const libCalls = () => paths().filter((p) => p.includes("scope=library")).length
const catCalls = () => paths().filter((p) => p.includes("scope=catalog")).length

beforeEach(() => {
  apiSpy.mockReset()
})

describe("useUnifiedSearch()", () => {
  it("does not call the library scope at 1 char", async () => {
    apiSpy.mockResolvedValue({})
    renderHook(() => useUnifiedSearch("a"), { wrapper: wrapper() })
    await new Promise((r) => setTimeout(r, 0))
    expect(libCalls()).toBe(0)
  })

  it("calls the library scope at ≥2 chars with q/scope/lang params", async () => {
    apiSpy.mockResolvedValue({})
    renderHook(() => useUnifiedSearch("godfather"), { wrapper: wrapper() })
    await waitFor(() => expect(libCalls()).toBe(1))
    expect(paths()[0]).toBe("/search?q=godfather&scope=library&lang=en-US")
  })

  it("defers the catalog scope until the library scope settles", async () => {
    let resolveLib!: (v: unknown) => void
    apiSpy.mockImplementation((path: string) => {
      if (path.includes("scope=library"))
        return new Promise((r) => {
          resolveLib = r as (v: unknown) => void
        })
      return Promise.resolve({})
    })
    renderHook(() => useUnifiedSearch("godfather"), { wrapper: wrapper() })
    await waitFor(() => expect(libCalls()).toBe(1))
    expect(catCalls()).toBe(0)
    await act(async () => {
      resolveLib({ series: [] })
    })
    await waitFor(() => expect(catCalls()).toBe(1))
    expect(paths().find((p) => p.includes("scope=catalog"))).toBe(
      "/search?q=godfather&scope=catalog&lang=en-US",
    )
  })

  it("does not call the catalog scope at only 2 chars", async () => {
    apiSpy.mockResolvedValue({ series: [] })
    renderHook(() => useUnifiedSearch("ab"), { wrapper: wrapper() })
    await waitFor(() => expect(libCalls()).toBe(1))
    await new Promise((r) => setTimeout(r, 0))
    expect(catCalls()).toBe(0)
  })

  it("dedups catalog items whose tmdb_id is in the same-type library group", async () => {
    apiSpy.mockImplementation((path: string) => {
      if (path.includes("scope=library"))
        return Promise.resolve({
          series: [{ id: 5, tmdb_id: 100, title: "Dup", source: "library" }],
        })
      return Promise.resolve({
        series: [
          { tmdb_id: 100, title: "Dup", source: "catalog" },
          { tmdb_id: 200, title: "Unique", source: "catalog" },
        ],
      })
    })
    const { result } = renderHook(() => useUnifiedSearch("dupe"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.catalog.series.length).toBe(1))
    expect(result.current.catalog.series[0]?.tmdbId).toBe(200)
    expect(result.current.library.series[0]?.id).toBe(5)
  })

  it("maps collections and dedups the catalog against the library group", async () => {
    apiSpy.mockImplementation((path: string) => {
      if (path.includes("scope=library"))
        return Promise.resolve({
          collections: [
            { tmdb_id: 10, name: "Lib Coll", source: "library" },
          ],
        })
      return Promise.resolve({
        collections: [
          { tmdb_id: 10, name: "Lib Coll", source: "catalog" },
          { tmdb_id: 20, name: "Cat Coll", source: "catalog" },
        ],
      })
    })
    const { result } = renderHook(() => useUnifiedSearch("coll"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.library.collections.length).toBe(1))
    expect(result.current.library.collections[0]?.tmdbId).toBe(10)
    await waitFor(() => expect(result.current.catalog.collections.length).toBe(1))
    expect(result.current.catalog.collections[0]?.tmdbId).toBe(20)
    expect(result.current.hasResults).toBe(true)
  })

  it("flips hasResults true when only collections match", async () => {
    apiSpy.mockImplementation((path: string) => {
      if (path.includes("scope=library"))
        return Promise.resolve({
          collections: [{ tmdb_id: 7, name: "Only Coll", source: "library" }],
        })
      return Promise.resolve({})
    })
    const { result } = renderHook(() => useUnifiedSearch("only"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.hasResults).toBe(true))
    expect(result.current.library.collections[0]?.name).toBe("Only Coll")
  })
})

describe("mapCollectionItems()", () => {
  it("maps a raw item, drops items missing name or tmdb_id, and coerces source", () => {
    const hits = mapCollectionItems(
      [
        { tmdb_id: 1, name: "Keep", poster_path: "abc", source: "library" },
        { name: "NoId" },
        { tmdb_id: 2 },
        { tmdb_id: 9, name: "Weird", source: "weird" },
      ],
      "catalog",
    )
    expect(hits.map((h) => h.name)).toEqual(["Keep", "Weird"])
    expect(hits[0]?.tmdbId).toBe(1)
    expect(hits[0]?.posterPath).toBe("abc")
    expect(hits[0]?.source).toBe("library")
    expect(hits[1]?.source).toBe("catalog")
  })
})

describe("mapSeriesItems()", () => {
  it("drops items missing a title or any id, and coerces source", () => {
    const hits = mapSeriesItems(
      [
        { id: 1, title: "Keep", source: "library" },
        { title: "NoId" },
        { id: 2 },
        { tmdb_id: 9, title: "Tmdb", source: "weird" },
      ],
      "catalog",
    )
    expect(hits.map((h) => h.title)).toEqual(["Keep", "Tmdb"])
    expect(hits[0]?.source).toBe("library")
    expect(hits[1]?.source).toBe("catalog")
  })
})

describe("resolveSearchPoster()", () => {
  it("proxies a library poster hash through /api/v1/media/ (never image.tmdb.org)", () => {
    const hit: SeriesHit = {
      kind: "series",
      source: "library",
      id: 1,
      title: "x",
      posterPath: "abc123hash",
    }
    const url = resolveSearchPoster(hit)
    expect(url).toBe("/api/v1/media/abc123hash")
    expect(url).not.toContain("image.tmdb.org")
  })

  it("proxies a catalog raw TMDB path through /api/v1/media/ (never image.tmdb.org)", () => {
    const hit: SeriesHit = {
      kind: "series",
      source: "catalog",
      tmdbId: 1,
      title: "x",
      posterPath: "/wZ123.jpg",
    }
    const url = resolveSearchPoster(hit)
    expect(url).toBe(`/api/v1/media/${encodeURIComponent("/wZ123.jpg")}`)
    expect(url).not.toContain("image.tmdb.org")
  })

  it("uses profile_path for people and returns undefined when empty", () => {
    const p: PersonHit = { kind: "person", source: "catalog", tmdbId: 1, name: "A" }
    expect(resolveSearchPoster(p)).toBeUndefined()
    const p2: PersonHit = { ...p, profilePath: "/prof.jpg" }
    expect(resolveSearchPoster(p2)).toBe(`/api/v1/media/${encodeURIComponent("/prof.jpg")}`)
  })
})
