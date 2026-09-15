// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SegmentedControl } from "./SegmentedControl";

afterEach(cleanup);

/** segmented.css on disk: this control's selected state lives in CSS. */
const segmentedCss = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "segmented.css"),
  "utf8",
);

const OPTIONS = [
  { id: "alpha", label: "Alpha" },
  { id: "bravo", label: "Bravo", hint: "The second one." },
  { id: "charlie", label: "Charlie" },
] as const;

describe("SegmentedControl", () => {
  it("shows every option and marks exactly one as pressed", () => {
    render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="bravo"
        onChange={() => {}}
      />,
    );
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual([
      "Alpha",
      "Bravo",
      "Charlie",
    ]);
    expect(
      buttons.filter((b) => b.getAttribute("aria-pressed") === "true"),
    ).toHaveLength(1);
    expect(
      screen
        .getByRole("button", { name: "Bravo" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
  });

  it("names the group, so the buttons are not three loose words", () => {
    render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole("group", { name: "Test group" })).toBeTruthy();
  });

  it("reports the option pressed", () => {
    const onChange = vi.fn();
    render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={onChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Charlie" }));
    expect(onChange).toHaveBeenCalledWith("charlie");
  });

  it("carries a hint as a title only where one was given", () => {
    render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={() => {}}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Bravo" }).getAttribute("title"),
    ).toBe("The second one.");
    expect(
      screen.getByRole("button", { name: "Alpha" }).getAttribute("title"),
    ).toBeNull();
  });

  it("draws one selected state, and it is the accent", () => {
    /*
     * This replaces a test asserting two variants — neutral for a control that
     * changes the view, accent for one that changes what data you see.
     *
     * The rule read well and drew badly. Measured against the reference, its
     * own range selector is a view control by that definition and still fills
     * the active segment with the accent; ours rendered a grey box that reads
     * as disabled rather than as chosen, and put two controls in one toolbar
     * disagreeing about what "selected" means.
     *
     * Asserted through the stylesheet rather than computed style: jsdom
     * applies no CSS, so a computed check here would pass against a deleted
     * rule.
     */
    render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={() => {}}
      />,
    );
    expect(
      screen.getByRole("group").getAttribute("data-variant"),
      "the variant hook is gone; one control, one selected state",
    ).toBeNull();

    const pressed = /\.segmented \.segmented-option\[aria-pressed="true"\]\s*\{([^}]*)\}/.exec(
      segmentedCss,
    );
    expect(pressed, "missing the selected-segment rule").not.toBeNull();
    expect(pressed?.[1], "the selection wears the accent").toMatch(
      /background:\s*var\(--accent\)/,
    );
    expect(pressed?.[1], "and its label keeps the contrast pair").toMatch(
      /color:\s*var\(--accent-ink\)/,
    );
  });

  it("gives the bar its own edge, so it reads as one control", () => {
    // Without it the segments floated in the topbar as loose buttons. The
    // reference draws a 1px frame around the group at 8px, with 2px of padding
    // and 6px segments inside it — concentric by §2.7 (8 − 2 = 6).
    const bar = /\.segmented\s*\{([^}]*)\}/.exec(segmentedCss);
    expect(bar, "missing the .segmented rule").not.toBeNull();
    // The control edge, not the static one: §2.9 gives `--border-control` to
    // anything clickable at rest, and the whole bar is one piece of chrome.
    expect(bar?.[1], "the bar needs its own outline").toMatch(
      /border:\s*1px solid var\(--border-control\)/,
    );
    expect(bar?.[1]).toMatch(/border-radius:\s*var\(--r-md\)/);

    const option = /\.segmented \.segmented-option\s*\{([^}]*)\}/.exec(
      segmentedCss,
    );
    expect(option?.[1], "the segment is one step inside the bar").toMatch(
      /border-radius:\s*var\(--r-sm\)/,
    );
  });
});
