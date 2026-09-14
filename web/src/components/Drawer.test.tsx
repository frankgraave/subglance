// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { useState } from "react";
import { Drawer } from "./Drawer";

afterEach(cleanup);

// Comments are stripped first: the prose quotes sizes and durations, and a
// rule about declarations should not be able to fail on a sentence.
const css = readFileSync("src/components/drawer.css", "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

/** A page with something to open the drawer from, so the focus return has a
 *  real element to come back to. */
function Harness() {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button type="button" onClick={() => setOpen(true)}>
        Open details
      </button>
      <Drawer open={open} onClose={() => setOpen(false)} title="Monitor details">
        <button type="button">Inside first</button>
        <button type="button">Inside last</button>
      </Drawer>
    </div>
  );
}

describe("Drawer", () => {
  it("renders nothing until it is opened", () => {
    render(<Harness />);
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open details" }));
    const panel = screen.getByRole("dialog", { name: "Monitor details" });
    expect(panel.getAttribute("aria-modal")).toBe("true");
    expect(document.querySelector(".drawer-scrim")).toBeTruthy();
  });

  it("closes on Escape and returns focus to the element that opened it", () => {
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "Open details" });
    // Focused first because a real pointer click focuses the button it hits,
    // while `fireEvent.click` does not — the drawer reads `activeElement`.
    opener.focus();
    fireEvent.click(opener);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    // Without this a keyboard user is left at the top of a page they cannot
    // orient in, because the drawer was covering it.
    expect(document.activeElement).toBe(opener);
  });

  it("moves focus into the panel on open", () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Open details" }));
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Close" }),
    );
  });

  it("traps Tab inside the panel in both directions", () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Open details" }));
    const panel = screen.getByRole("dialog");
    const first = screen.getByRole("button", { name: "Close" });
    const last = screen.getByRole("button", { name: "Inside last" });

    last.focus();
    fireEvent.keyDown(panel, { key: "Tab" });
    expect(document.activeElement).toBe(first);

    first.focus();
    fireEvent.keyDown(panel, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it("dismisses from the scrim and from the close control", () => {
    const onClose = vi.fn();
    render(
      <Drawer open onClose={onClose} title="Monitor details">
        <p>Body</p>
      </Drawer>,
    );
    fireEvent.click(document.querySelector(".drawer-scrim")!);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("moves with the panel duration and drops the slide under reduced motion", () => {
    expect(css).toMatch(/animation:\s*drawer-in var\(--dur-panel\) var\(--ease\)/);
    const reduced = css.match(
      /@media \(prefers-reduced-motion: reduce\)\s*\{[\s\S]*?\n\}/,
    )![0];
    // The accommodation removes the movement, never the panel itself.
    expect(reduced).toMatch(/animation:\s*none/);
    expect(reduced).not.toMatch(/display:\s*none/);
  });

  it("spells out no literal colour or duration", () => {
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/rgba?\(/);
    expect(css).not.toMatch(/\b\d+m?s\b/);
    expect(css).toMatch(/background:\s*var\(--scrim\)/);
  });
});
