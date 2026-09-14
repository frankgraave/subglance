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
    expect(document.querySelector(".icon-tile")!.getAttribute("data-tone")).toBe(
      "neutral",
    );
    rerender(
      <IconTile tone="down">
        <svg />
      </IconTile>,
    );
    expect(document.querySelector(".icon-tile")!.getAttribute("data-tone")).toBe(
      "down",
    );
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

  it("draws no border, because the fill already separates the tile", () => {
    // The whole point of the tile: a plane needs no edge. A future "just add a
    // hairline" edit has to fail here first.
    expect(css).not.toMatch(/^\s*border\s*:/m);
    expect(css).not.toMatch(/border-(top|right|bottom|left)\s*:/);
  });

  it("spells out no literal colour, radius or size", () => {
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/rgba?\(/);
    // Every length in the file comes from a token.
    const lengths = css.match(/:\s*[^;]*?\b\d+(px|rem|em)\b/g) ?? [];
    expect(lengths).toEqual([]);
    expect(css).toMatch(/border-radius:\s*var\(--r-sm\)/);
  });
});
