import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"

import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from "./accordion"

function Sample({ type }: { type: "single" | "multiple" }) {
  return (
    <Accordion {...(type === "single" ? { type, collapsible: true } : { type })}>
      <AccordionItem value="a">
        <AccordionTrigger>A</AccordionTrigger>
        <AccordionContent>body-a</AccordionContent>
      </AccordionItem>
      <AccordionItem value="b">
        <AccordionTrigger>B</AccordionTrigger>
        <AccordionContent>body-b</AccordionContent>
      </AccordionItem>
    </Accordion>
  )
}

describe("<Accordion />", () => {
  it("type=multiple keeps both items open", async () => {
    const u = userEvent.setup()
    render(<Sample type="multiple" />)
    await u.click(screen.getByText("A"))
    await u.click(screen.getByText("B"))
    expect(await screen.findByText("body-a")).toBeInTheDocument()
    expect(await screen.findByText("body-b")).toBeInTheDocument()
  })

  it("type=single collapsible toggles open/closed on same trigger", async () => {
    const u = userEvent.setup()
    render(<Sample type="single" />)
    const trigger = screen.getByText("A")
    await u.click(trigger)
    expect(await screen.findByText("body-a")).toBeInTheDocument()
    await u.click(trigger)
    // After collapse, Radix removes the content from the tree on closed state
    // when collapsible=true. Querying must return null.
    expect(screen.queryByText("body-a")).toBeNull()
  })
})
