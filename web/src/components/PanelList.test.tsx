// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PanelList, PanelRow } from "./PanelList";

/**
 * SUB-106 §10: a list is a stack of panels, not a ruled table.
 *
 * What is asserted here is the structure the stylesheet hangs off, because
 * that is the half that can regress silently. The visual rules themselves —
 * one border per row, the corner, the gap — are stated once in panellist.css
 * and guarded by the token suite; what a unit test can hold down is that every
 * row is still its own list item with its own slots, that actions stay in the
 * DOM when nobody is hovering, and that the list is still a list.
 */

afterEach(cleanup);

describe("PanelList", () => {
  it("renders a real list, so the item count still matches the screen", () => {
    render(
      <PanelList label="Three things">
        <PanelRow>one</PanelRow>
        <PanelRow>two</PanelRow>
        <PanelRow>three</PanelRow>
      </PanelList>,
    );
    expect(screen.getByRole("list", { name: "Three things" })).toBeTruthy();
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
  });

  it("gives every row its own panel rather than a shared divider", () => {
    // The structural half of "each row owns its edge": a row is one element
    // carrying the class the border is drawn on, not a cell inside a grid.
    const { container } = render(
      <PanelList>
        <PanelRow>one</PanelRow>
        <PanelRow>two</PanelRow>
      </PanelList>,
    );
    const rows = container.querySelectorAll(".panel-row");
    expect(rows).toHaveLength(2);
    for (const row of rows) {
      expect(row.tagName).toBe("LI");
    }
  });

  it("keeps a row's actions mounted when the pointer is elsewhere", () => {
    // They are revealed by opacity, never by unmounting: removing them would
    // resize the row under the pointer and would put them out of reach of a
    // keyboard entirely.
    render(
      <PanelList>
        <PanelRow actions={<button type="button">Pause</button>}>api</PanelRow>
      </PanelList>,
    );
    expect(screen.getByRole("button", { name: "Pause" })).toBeTruthy();
  });

  it("puts the icon in its own slot, because hover is expressed there", () => {
    const { container } = render(
      <PanelList>
        <PanelRow icon={<span data-testid="lamp" />}>api</PanelRow>
      </PanelList>,
    );
    const icon = container.querySelector(".panel-row-icon");
    expect(icon).not.toBeNull();
    expect(icon?.querySelector("[data-testid='lamp']")).not.toBeNull();
  });

  it("omits the icon and action slots when a row has neither", () => {
    const { container } = render(
      <PanelList>
        <PanelRow>api</PanelRow>
      </PanelList>,
    );
    expect(container.querySelector(".panel-row-icon")).toBeNull();
    expect(container.querySelector(".panel-row-actions")).toBeNull();
  });

  it("passes status through as an attribute a stylesheet can key off", () => {
    render(
      <PanelList>
        <PanelRow status="down" data-testid="row-api">
          api
        </PanelRow>
      </PanelList>,
    );
    expect(screen.getByTestId("row-api").getAttribute("data-status")).toBe(
      "down",
    );
  });
});
