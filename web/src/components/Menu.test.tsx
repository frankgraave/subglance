// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { Menu } from "./Menu";

afterEach(cleanup);

// Comments are stripped first: the prose quotes sizes and colours, and a rule
// about declarations should not be able to fail on a sentence.
const css = readFileSync("src/components/menu.css", "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

const items = [
  {
    key: "pause",
    title: "Pause",
    description: "Stops checks and alerts until resumed.",
  },
  {
    key: "reset",
    title: "Reset statistics",
    description: "Clears the recorded uptime history.",
  },
  {
    key: "remove",
    title: "Remove",
    description: "Deletes the monitor and its history.",
    tone: "danger" as const,
  },
];

function openMenu() {
  const trigger = screen.getByRole("button", { name: "Actions" });
  fireEvent.click(trigger);
  return trigger;
}

function press(key: string) {
  fireEvent.keyDown(screen.getByRole("menu"), { key });
}

describe("Menu", () => {
  it("gives every item a description, not a bare verb", () => {
    render(<Menu trigger="Actions" items={items} />);
    openMenu();
    for (const item of items) {
      expect(screen.getByText(item.title)).toBeTruthy();
      expect(screen.getByText(item.description)).toBeTruthy();
    }
    expect(document.querySelectorAll(".menu-item-description")).toHaveLength(3);
  });

  it("moves through the items with the arrow keys and wraps", () => {
    render(<Menu trigger="Actions" items={items} />);
    openMenu();
    const options = screen.getAllByRole("menuitem");
    expect(document.activeElement).toBe(options[0]);
    press("ArrowDown");
    expect(document.activeElement).toBe(options[1]);
    press("ArrowUp");
    press("ArrowUp");
    expect(document.activeElement).toBe(options[2]);
    press("Home");
    expect(document.activeElement).toBe(options[0]);
    press("End");
    expect(document.activeElement).toBe(options[2]);
  });

  it("closes on Escape and hands focus back to the trigger", () => {
    render(<Menu trigger="Actions" items={items} />);
    const trigger = openMenu();
    expect(screen.queryByRole("menu")).toBeTruthy();
    press("Escape");
    expect(screen.queryByRole("menu")).toBeNull();
    // Without this a keyboard user is dropped at the top of the document.
    expect(document.activeElement).toBe(trigger);
  });

  it("skips a disabled item when arrowing and reports the action chosen", () => {
    const onSelect = vi.fn();
    render(
      <Menu
        trigger="Actions"
        items={[
          { ...items[0], onSelect },
          { ...items[1], disabled: true },
          items[2],
        ]}
      />,
    );
    openMenu();
    const options = screen.getAllByRole("menuitem");
    // Arrowing onto something that cannot be chosen is a dead end, so the
    // disabled item is read but never focused.
    press("ArrowDown");
    expect(document.activeElement).toBe(options[2]);
    press("ArrowDown");
    expect(document.activeElement).toBe(options[0]);
    fireEvent.click(options[0]);
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("shows focus with the accent ring rather than a neutral outline", () => {
    const rule = css.match(/\.menu-item:focus-visible\s*\{[^}]*\}/)![0];
    expect(rule).toMatch(/var\(--accent-ring\)/);
    // The default outline is cleared only because the ring replaces it; any
    // other outline value here would be a second focus language.
    for (const declaration of rule.match(/outline:[^;]+/g) ?? []) {
      expect(declaration.trim()).toBe("outline: none");
    }
  });

  it("spells out no literal colour or duration", () => {
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/rgba?\(/);
    expect(css).not.toMatch(/\b\d+m?s\b/);
    expect(css).toMatch(/var\(--dur-hover\)/);
  });
});
