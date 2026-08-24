import { describe, expect, it, vi } from "vitest"
import userEvent from "@testing-library/user-event"
import { screen } from "@testing-library/react"

import { renderWithProviders } from "@/test-utils"
import { CommandPalette } from "./CommandPalette"

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
})
