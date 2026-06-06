import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"

import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "./sheet"

describe("<Sheet />", () => {
  it("opens on trigger click and mounts content under portal", async () => {
    const u = userEvent.setup()
    render(
      <Sheet>
        <SheetTrigger>open</SheetTrigger>
        <SheetContent side="right">
          <SheetHeader>
            <SheetTitle>title-here</SheetTitle>
          </SheetHeader>
          <p>body-here</p>
        </SheetContent>
      </Sheet>,
    )
    await u.click(screen.getByText("open"))
    expect(await screen.findByText("title-here")).toBeInTheDocument()
    expect(screen.getByText("body-here")).toBeInTheDocument()
  })

  it("fires onOpenChange(false) when close button is clicked", async () => {
    const u = userEvent.setup()
    const handler = vi.fn()
    render(
      <Sheet defaultOpen onOpenChange={handler}>
        <SheetContent side="right">
          <SheetHeader>
            <SheetTitle>sheet</SheetTitle>
          </SheetHeader>
          <p>content</p>
        </SheetContent>
      </Sheet>,
    )
    const closeBtn = await screen.findByRole("button", { name: /close/i })
    await u.click(closeBtn)
    expect(handler).toHaveBeenCalledWith(false)
  })
})
