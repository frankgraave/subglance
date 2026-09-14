// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { IconTile } from "./IconTile";

afterEach(cleanup);

// Comments are stripped first: the prose in these files quotes contrast
// ratios and pixel sizes, and a rule about declarations should not be able to
// fail on a sentence.
const css = readFileSync("src/components/icontile.css", "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

describe("IconTile", () => {
  it("hides a decorative tile from assistive technology", () => {
    render(
      <IconTile>
        <svg data-testid="glyph" />
      </IconTile>,
    );
    const tile = document.querySelector(".icon-tile")!;
    expect(tile.getAttribute("aria-hidden")).toBe("true");
    expect(tile.getAttribute("role")).toBeNull();
    expect(screen.getByTestId("glyph")).toBeTruthy();
  });

  it("becomes an image with a name when the tile is the only label", () => {
    render(
      <IconTile label="HTTP monitor">
        <svg />
      </IconTile>,
    );
    const tile = screen.getByRole("img", { name: "HTTP monitor" });
    expect(tile.getAttribute("aria-hidden")).toBeNull();
  });

  it("defaults to the neutral tone and carries the tone it is given", () => {
    const { rerender } = render(
      <IconTile>
        <svg />
      </IconTile>,
    );
    expect(
      document.querySelector(".icon-tile")!.getAttribute("data-tone"),
    ).toBe("neutral");
    rerender(
      <IconTile tone="down">
        <svg />
      </IconTile>,
    );
    expect(
      document.querySelector(".icon-tile")!.getAttribute("data-tone"),
    ).toBe("down");
  });

  it("keeps the extra class beside the base class rather than replacing it", () => {
    render(
      <IconTile className="card-head-tile">
        <svg />
      </IconTile>,
    );
    const tile = document.querySelector(".icon-tile")!;
    expect(tile.classList.contains("card-head-tile")).toBe(true);
  });

  it("draws the border that makes the tile a miniature of the card", () => {
    // This assertion is the inverse of the one it replaces, and the reversal
    // is the point. The tile was borderless on the reasoning that a filled
    // plane needs no edge. Measured against the reference style, the tile
    // there carries a 1px border at 24px square — and it works because the
    // tile is not a plane, it is a small version of the card it sits in: same
    // edge, same radius family, one size down. That is what makes it read as
    // part of the header rather than as a sticker on it.
    expect(css).toMatch(/border:\s*1px solid var\(--border-control\)/);
    expect(css).toMatch(/border-radius:\s*var\(--r-sm\)/);
  });

  it("keeps the glyph at half the tile, so the border never crowds it", () => {
    // 12px in 24px. At --space-4 (16px) the glyph reaches within 4px of the
    // border on every side and the tile reads as a cramped box rather than as
    // a frame around a mark.
    const glyph = /\.icon-tile\s*>\s*svg\s*\{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(glyph).toMatch(/width:\s*var\(--space-3\)/);
    expect(glyph).toMatch(/height:\s*var\(--space-3\)/);
  });

  it("spells out no literal colour, radius or size", () => {
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/rgba?\(/);
    // Every length in the file comes from a token, with one exception: the
    // border's 1px. A hairline is not a step on the spacing ladder — it is the
    // thinnest line the display can draw — so tokenising it would invent a
    // scale with a single rung.
    const lengths = (css.match(/:\s*[^;]*?\b\d+(px|rem|em)\b/g) ?? []).filter(
      (l) => l.trim() !== ": 1px",
    );
    expect(lengths).toEqual([]);
    expect(css).toMatch(/border-radius:\s*var\(--r-sm\)/);
  });
});
