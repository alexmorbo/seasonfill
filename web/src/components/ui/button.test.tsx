import { describe, expect, it } from "vitest"
import { render } from "@testing-library/react"

import { Button } from "./button"

describe("<Button />", () => {
  it("default variant carries bg-primary", () => {
    const { getByRole } = render(<Button>go</Button>)
    expect(getByRole("button").className).toContain("bg-primary")
  })

  it("primary variant carries bg-accent + accent-text", () => {
    const { getByRole } = render(<Button variant="primary">go</Button>)
    const cls = getByRole("button").className
    expect(cls).toContain("bg-accent")
    expect(cls).toContain("text-accent-text")
  })

  it("ghost variant is transparent", () => {
    const { getByRole } = render(<Button variant="ghost">go</Button>)
    expect(getByRole("button").className).toContain("bg-transparent")
  })

  it("icon-btn size is 30x30 square", () => {
    const { getByRole } = render(<Button size="icon-btn">x</Button>)
    const cls = getByRole("button").className
    expect(cls).toContain("h-[30px]")
    expect(cls).toContain("w-[30px]")
  })

  it("sm size is h-7", () => {
    const { getByRole } = render(<Button size="sm">x</Button>)
    expect(getByRole("button").className).toContain("h-7")
  })
})
