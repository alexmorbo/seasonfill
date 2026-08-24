import { describe, expect, it, vi, beforeEach } from "vitest"
import userEvent from "@testing-library/user-event"
import { screen } from "@testing-library/react"

import { renderWithProviders } from "@/test-utils"
import type { UnifiedSearchResult, SearchGroup } from "@/api/search"

const mockNavigate = vi.fn()
vi.mock("react-router-dom", async () => {
  const actual =
    await vi.importActual<typeof import("react-router-dom")>("react-router-dom")
  return { ...actual, useNavigate: () => mockNavigate }
})

const emptyGroup: SearchGroup = { series: [], movies: [], collections: [], people: [] }
const baseResult: UnifiedSearchResult = {
  library: emptyGroup,
  catalog: emptyGroup,
  libraryLoading: false,
  catalogSearching: false,
  hasResults: false,
  enabled: false,
}
let mockResult: UnifiedSearchResult = baseResult

vi.mock("@/api/search", async () => {
  const actual = await vi.importActual<typeof import("@/api/search")>("@/api/search")
  return {
    ...actual,
    useUnifiedSearch: () => mockResult,
  }
})

import { CommandPalette } from "./CommandPalette"

beforeEach(() => {
  mockResult = baseResult
  mockNavigate.mockClear()
})

describe("<CommandPalette />", () => {
  it("renders the Actions heading and the New scan command when open", async () => {
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    expect(await screen.findByText("Actions")).toBeInTheDocument()
    expect(screen.getByText("New scan")).toBeInTheDocument()
  })

  it("calls onNewScan when the New scan item is clicked", async () => {
    const onNewScan = vi.fn()
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={onNewScan} />,
    )
    await userEvent.click(await screen.findByText("New scan"))
    expect(onNewScan).toHaveBeenCalledTimes(1)
  })

  it("renders nothing when closed", () => {
    renderWithProviders(
      <CommandPalette open={false} onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    expect(screen.queryByText("New scan")).toBeNull()
  })

  it("renders the Library group before the Catalog group, caps each type at 3, and omits empty groups", async () => {
    const mkSeries = (n: number, source: "library" | "catalog") =>
      Array.from({ length: n }, (_, i) => ({
        kind: "series" as const,
        source,
        id: source === "library" ? i + 1 : undefined,
        tmdbId: 1000 + i,
        title: `${source} S${i}`,
      }))
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: { series: mkSeries(5, "library"), movies: [], collections: [], people: [] },
      catalog: { series: mkSeries(2, "catalog"), movies: [], collections: [], people: [] },
    }
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    const lib = await screen.findByText("Library", {
      selector: "[cmdk-group-heading]",
    })
    const cat = screen.getByText("Catalog", {
      selector: "[cmdk-group-heading]",
    })
    expect(
      lib.compareDocumentPosition(cat) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.getAllByTestId("search-result-row")).toHaveLength(5)
    expect(screen.queryByText("Collections")).toBeNull()
  })

  it("shows the searching-catalog affordance while the catalog call is in flight", async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      catalogSearching: true,
      library: {
        series: [
          { kind: "series", source: "library", id: 1, tmdbId: 1, title: "L" },
        ],
        movies: [],
        collections: [],
        people: [],
      },
    }
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    expect(await screen.findByTestId("cmdk-catalog-searching")).toBeInTheDocument()
  })

  it("shows the empty state when a settled search has no results", async () => {
    mockResult = { ...baseResult, enabled: true, hasResults: false }
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    expect(await screen.findByTestId("cmdk-search-empty")).toBeInTheDocument()
  })

  it("suppresses the empty state while the catalog is still searching", async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: false,
      catalogSearching: true,
    }
    renderWithProviders(
      <CommandPalette open onOpenChange={vi.fn()} onNewScan={vi.fn()} />,
    )
    expect(await screen.findByTestId("cmdk-catalog-searching")).toBeInTheDocument()
    expect(screen.queryByTestId("cmdk-search-empty")).toBeNull()
  })

  it("navigates to /collections/:tmdbId and closes the palette when a collection row is selected", async () => {
    const onOpenChange = vi.fn()
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: {
        series: [],
        movies: [],
        collections: [
          {
            kind: "collection",
            source: "library",
            tmdbId: 603,
            name: "The Matrix Collection",
          },
        ],
        people: [],
      },
    }
    renderWithProviders(
      <CommandPalette open onOpenChange={onOpenChange} onNewScan={vi.fn()} />,
    )
    await userEvent.click(await screen.findByText("The Matrix Collection"))
    expect(mockNavigate).toHaveBeenCalledWith("/collections/603")
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("navigates to /search and closes the palette when 'show all' is selected", async () => {
    mockResult = { ...baseResult, enabled: true, hasResults: false }
    const onOpenChange = vi.fn()
    renderWithProviders(
      <CommandPalette open onOpenChange={onOpenChange} onNewScan={vi.fn()} />,
    )
    const input = await screen.findByPlaceholderText("Search, navigate, actions")
    await userEvent.type(input, "matrix")

    const showAll = await screen.findByTestId("cmdk-show-all")
    await userEvent.click(showAll)

    expect(mockNavigate).toHaveBeenCalledWith("/search?q=matrix")
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
