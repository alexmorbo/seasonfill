import { describe, expect, it } from "vitest"
import { render } from "@testing-library/react"

import { Badge } from "./badge"

const VARIANTS = ["ok", "warn", "danger", "accent", "info", "neutral", "solid"] as const

describe("<Badge />", () => {
  it("renders the default variant with primary tokens", () => {
    const { container } = render(<Badge>hello</Badge>)
    const el = container.firstElementChild as HTMLElement
    expect(el.className).toContain("bg-primary")
  })

  it.each(VARIANTS)("renders %s variant with text-token class", (v) => {
    const { container } = render(<Badge variant={v}>x</Badge>)
    const el = container.firstElementChild as HTMLElement
    if (v === "neutral" || v === "solid") {
      expect(el.className).toContain("text-tx-secondary")
    } else {
      expect(el.className).toContain(`text-${v}`)
    }
  })

  it("mono flag adds font-mono", () => {
    const { container } = render(<Badge variant="ok" mono>x</Badge>)
    const el = container.firstElementChild as HTMLElement
    expect(el.className).toContain("font-mono")
  })

  it("forwards arbitrary className", () => {
    const { container } = render(<Badge className="extra-1">x</Badge>)
    const el = container.firstElementChild as HTMLElement
    expect(el.className).toContain("extra-1")
  })
})
