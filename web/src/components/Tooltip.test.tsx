// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Tooltip } from "./Tooltip";

afterEach(cleanup);

describe("Tooltip", () => {
  it("names the unit once in the header instead of on every row", () => {
    render(
      <Tooltip
        timestamp="12:00:00"
        unit="latency"
        rows={[
          { key: "a", label: "Slowest", value: "120 ms", marker: "up" },
          { key: "b", label: "Median", value: "90 ms", marker: "up" },
        ]}
      />,
    );
    expect(screen.getByText("latency")).toBeTruthy();
    // The header sits above the rows and is separated from them, so the rows
    // read as entries under a caption rather than as the first entry.
    const head = document.querySelector(".tooltip-head")!;
    expect(within(head as HTMLElement).getByText("12:00:00")).toBeTruthy();
    expect(document.querySelectorAll(".tooltip-row")).toHaveLength(2);
  });

  it("keeps the marker column on a row that has no status", () => {
    render(
      <Tooltip
        timestamp="12:00:00"
        rows={[{ key: "a", label: "Checks", value: "40" }]}
      />,
    );
    // A neutral dot beside a coloured one would read as a status of its own,
    // so the row gets an empty spacer and the labels still line up.
    expect(document.querySelectorAll(".tooltip-marker")).toHaveLength(0);
    expect(document.querySelectorAll(".tooltip-marker-gap")).toHaveLength(1);
  });

  it("draws the markers from the status the row carries", () => {
    render(
      <Tooltip
        timestamp="12:00:00"
        rows={[
          { key: "a", label: "Slowest", value: "1", marker: "up" },
          { key: "b", label: "Failed", value: "2", marker: "down" },
        ]}
      />,
    );
    const markers = [...document.querySelectorAll(".tooltip-marker")].map((m) =>
      m.getAttribute("data-status"),
    );
    expect(markers).toEqual(["up", "down"]);
    // Decorative: the label beside it already says which series it is.
    expect(
      document.querySelector(".tooltip-marker")!.getAttribute("aria-hidden"),
    ).toBe("true");
  });

  it("sets the total apart from the rows it closes", () => {
    render(
      <Tooltip
        timestamp="12:00:00"
        rows={[{ key: "a", label: "Slowest", value: "120 ms", marker: "up" }]}
        total={{ label: "Total", value: "40 checks" }}
      />,
    );
    const total = document.querySelector(".tooltip-row--total")!;
    expect(within(total as HTMLElement).getByText("40 checks")).toBeTruthy();
    expect(total.classList.contains("tooltip-row")).toBe(true);
  });

  it("says partial data in words on a dashed chip, and nothing when whole", () => {
    const { rerender } = render(
      <Tooltip timestamp="12:00" rows={[]} partial />,
    );
    // Words, not a colour or a dash alone: the chip has to survive a
    // screenshot in greyscale and a screen reader reading it aloud.
    const chip = screen.getByText("Partial data");
    expect(chip.classList.contains("chip--state")).toBe(true);

    rerender(<Tooltip timestamp="12:00" rows={[]} />);
    expect(screen.queryByText("Partial data")).toBeNull();
  });

  it("omits the rows block entirely when there are no rows", () => {
    render(<Tooltip timestamp="12:00" rows={[]} />);
    expect(document.querySelector(".tooltip-rows")).toBeNull();
  });
});
