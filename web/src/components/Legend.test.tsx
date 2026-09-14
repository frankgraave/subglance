// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { Legend } from "./Legend";

afterEach(cleanup);

// Comments are stripped first: the prose in these files quotes contrast
// ratios and pixel sizes, and a rule about declarations should not be able to
// fail on a sentence.
const css = readFileSync("src/components/legend.css", "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

describe("Legend", () => {
  it("pairs each label with its value as a term and a definition", () => {
    render(
      <Legend
        label="Status share"
        items={[
          { key: "up", label: "Up", marker: "up", value: "98.2%" },
          { key: "down", label: "Down", marker: "down", value: "1.8%" },
        ]}
      />,
    );
    expect(document.querySelectorAll(".legend-item")).toHaveLength(2);
    expect(document.querySelectorAll("dt.legend-label")).toHaveLength(2);
    expect(document.querySelectorAll("dd.legend-value")).toHaveLength(2);
    expect(screen.getByText("98.2%")).toBeTruthy();
  });

  it("draws the marker from the status the item carries", () => {
    render(
      <Legend
        items={[
          { key: "a", label: "Warn", marker: "warn" },
          { key: "b", label: "Other" },
        ]}
      />,
    );
    const markers = [...document.querySelectorAll(".legend-marker")];
    expect(markers.map((m) => m.getAttribute("data-status"))).toEqual([
      "warn",
      null,
    ]);
    // The mark repeats what the label says, so it must not be announced twice.
    expect(markers.every((m) => m.getAttribute("aria-hidden") === "true")).toBe(
      true,
    );
  });

  it("drops the value column on a label-only row", () => {
    render(<Legend items={[{ key: "a", label: "Checks" }]} />);
    const item = document.querySelector(".legend-item")!;
    expect(item.classList.contains("legend-item--flow")).toBe(true);
    expect(document.querySelectorAll(".legend-value")).toHaveLength(0);
  });

  it("is quiet through size, face and casing — never through faded ink", () => {
    // The regression this guards: reaching for the weakest ink to make the
    // legend recede, which spends contrast the label cannot afford.
    expect(css).not.toMatch(/var\(--ink-4\)/);
    expect(css).not.toMatch(/\.legend-label\s*\{[^}]*--ink-3/);
    // The quietness comes from the shared role, which sets 12px mono uppercase.
    expect(css).toMatch(/\.legend-label\s*\{\s*@apply caps-legend;/);
  });

  it("spells out no literal colour or length", () => {
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/rgba?\(/);
    const lengths = css.match(/:\s*[^;]*?\b\d+(px|rem|em)\b/g) ?? [];
    expect(lengths).toEqual([]);
  });
});
