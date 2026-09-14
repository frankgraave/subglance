// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import {
  CountChip,
  EmptyAvatar,
  IconTile,
  MetaChip,
  StateChip,
  StatusChip,
} from "./Chip";

afterEach(cleanup);

describe("StatusChip", () => {
  it("carries the status as data and the word as text", () => {
    // Colour never stands alone (DESIGN.md §2.3): the attribute is what the
    // stylesheet paints, the word is what everyone else reads.
    const { container } = render(<StatusChip status="down">Down</StatusChip>);
    const chip = container.firstElementChild;
    expect(chip?.getAttribute("data-status")).toBe("down");
    expect(chip?.textContent).toBe("Down");
    expect(chip?.className).toContain("chip--status");
  });
});

describe("MetaChip", () => {
  it("keeps label and value as separate elements, so a divider can sit between", () => {
    // If they collapsed into one string the internal divider would have
    // nothing to draw on, and the chip would be a sentence with a colon.
    const { container } = render(<MetaChip label="Region" value="eu-west" />);
    expect(container.querySelector(".chip-label")?.textContent).toBe("Region");
    expect(container.querySelector(".chip-value")?.textContent).toBe("eu-west");
  });
});

describe("CountChip", () => {
  it("renders the count", () => {
    const { container } = render(<CountChip>12</CountChip>);
    expect(container.firstElementChild?.textContent).toBe("12");
  });
});

describe("StateChip", () => {
  it("is a separate kind from the status badge", () => {
    // The whole convention rests on these two never being the same element:
    // one is a reading, the other is a statement about the reading.
    const { container } = render(<StateChip>Partial data</StateChip>);
    const chip = container.firstElementChild;
    expect(chip?.className).toContain("chip--state");
    expect(chip?.className).not.toContain("chip--status");
    expect(chip?.getAttribute("data-status")).toBeNull();
  });
});

describe("EmptyAvatar", () => {
  it("announces the absence rather than being skipped", () => {
    // An unlabelled empty span is invisible to a screen reader, and "nobody
    // assigned" is information — the page would differ from the one on screen.
    render(<EmptyAvatar />);
    expect(screen.getByRole("img", { name: "Nobody assigned" })).toBeTruthy();
  });

  it("takes a caller's wording", () => {
    render(<EmptyAvatar label="No owner yet" />);
    expect(screen.getByRole("img", { name: "No owner yet" })).toBeTruthy();
  });
});

describe("IconTile", () => {
  it("hides a decorative glyph from assistive technology", () => {
    // A tile beside a monitor's name that announces "globe" adds a word
    // carrying no information.
    const { container } = render(<IconTile>*</IconTile>);
    const tile = container.firstElementChild;
    expect(tile?.getAttribute("aria-hidden")).toBe("true");
    expect(tile?.getAttribute("role")).toBeNull();
  });

  it("announces a glyph that is the only thing naming the row", () => {
    render(<IconTile label="HTTP monitor">*</IconTile>);
    const tile = screen.getByRole("img", { name: "HTTP monitor" });
    expect(tile.getAttribute("aria-hidden")).toBeNull();
  });
});
