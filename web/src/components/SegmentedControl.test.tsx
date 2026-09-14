// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SegmentedControl } from "./SegmentedControl";

afterEach(cleanup);

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

  it("defaults to the view variant and takes the data variant when asked", () => {
    // The variant is the whole reason the component exists rather than three
    // copies: a control that changes *what data* you look at wears the accent,
    // one that changes *how* it is drawn stays neutral (DESIGN.md §7.8).
    const { rerender } = render(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole("group").getAttribute("data-variant")).toBe("view");
    rerender(
      <SegmentedControl
        label="Test group"
        options={OPTIONS}
        value="alpha"
        onChange={() => {}}
        variant="data"
      />,
    );
    expect(screen.getByRole("group").getAttribute("data-variant")).toBe("data");
  });
});
