import { describe, expect, it } from "vitest"
import { render } from "@testing-library/react"

import { Sparkline } from "./sparkline"

describe("<Sparkline />", () => {
  it("renders one bar per data point", () => {
    const { container } = render(<Sparkline data={[1, 2, 3, 4]} />)
    expect(container.querySelectorAll("[data-value]").length).toBe(4)
  })

  it("marks the highest bar as peak by default", () => {
    const { container } = render(<Sparkline data={[1, 9, 2]} />)
    const bars = container.querySelectorAll("[data-value]")
    expect(bars[1]!.getAttribute("data-peak")).toBe("true")
    expect(bars[0]!.getAttribute("data-peak")).toBe("false")
  })

  it("honors an explicit peakIndex prop", () => {
    const { container } = render(<Sparkline data={[5, 1, 1]} peakIndex={2} />)
    const bars = container.querySelectorAll("[data-value]")
    expect(bars[2]!.getAttribute("data-peak")).toBe("true")
  })

  it("renders a skeleton placeholder for empty data", () => {
    const { container } = render(<Sparkline data={[]} ariaLabel="empty" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.getAttribute("data-empty")).toBe("true")
    expect(el.getAttribute("aria-label")).toBe("empty")
  })

  it("forwards className", () => {
    const { container } = render(<Sparkline data={[1, 2]} className="my-spark" />)
    expect((container.firstElementChild as HTMLElement).className).toContain("my-spark")
  })
})
