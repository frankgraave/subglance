// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { createQueryClient } from "../live/queryClient";
import { CommandMenu } from "./CommandMenu";

/*
 * What the menu shows besides the commands themselves: a field that says
 * what it searches, the results under named groups, each monitor's state,
 * and the keys that drive it.
 */
const rows = [
  { id: 1, name: "Payment API", type: "http", target: "https://payments.example", enabled: true, status: "down", interval_s: 60, timeout_s: 10 },
  { id: 2, name: "Docs site", type: "http", target: "https://docs.example", enabled: true, status: "up", interval_s: 60, timeout_s: 10 },
];
const props = (canWrite = true) => ({ client: createQueryClient(), canWrite, onClose: vi.fn(), onOpenMonitor: vi.fn(), onNavigate: vi.fn(), onAddMonitor: vi.fn(), onThemeChange: vi.fn() });
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(JSON.stringify({ monitors: rows }), { status: 200 }))));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const groupNames = () => screen.getAllByRole("group").map((g) => g.getAttribute("aria-labelledby") && document.getElementById(g.getAttribute("aria-labelledby")!)?.textContent);

it("says what the field searches before anything is typed", () => {
  render(<CommandMenu {...props()} />);
  expect(screen.getByRole("combobox").getAttribute("placeholder")).toBe("Search monitors or commands\u2026");
});

it("puts every result under a named group, monitors first, and walks them in that order", async () => {
  render(<CommandMenu {...props()} />);
  await screen.findByRole("option", { name: /Open Payment API/ });
  expect(groupNames()).toEqual(["Monitors", "Actions", "Navigation", "Theme"]);
  const [monitors, actions, navigation, theme] = screen.getAllByRole("group");
  expect(within(monitors).getAllByRole("option").map((o) => o.textContent)).toEqual(["Open Payment API Down \u00b7 https://payments.example", "Open Docs site Up \u00b7 https://docs.example"]);
  expect(within(actions).getAllByRole("option").map((o) => o.textContent)).toEqual(["Pause Payment API", "Pause Docs site", "Add monitor"]);
  expect(within(navigation).getAllByRole("option")).toHaveLength(5);
  expect(within(theme).getAllByRole("option")).toHaveLength(3);
  // The arrows walk the options in the order they are drawn, across every
  // heading: an index kept in a different order from the groups would make
  // the highlight jump back over a heading.
  const input = screen.getByRole("combobox");
  const active = () => document.getElementById(input.getAttribute("aria-activedescendant")!);
  for (const option of screen.getAllByRole("option")) {
    expect(active()).toBe(option);
    fireEvent.keyDown(input, { key: "ArrowDown" });
  }
  expect(active()).toBe(screen.getAllByRole("option")[0]);
});

it("shows each monitor's state as a lamp and a word, and finds monitors by it", async () => {
  render(<CommandMenu {...props()} />);
  const payment = await screen.findByRole("option", { name: "Open Payment API Down \u00b7 https://payments.example" });
  expect(payment.querySelector('.led[data-status="down"]')).not.toBeNull();
  // The lamp is decoration beside the word; it adds nothing to the name.
  expect(payment.querySelector(".led")?.getAttribute("aria-hidden")).toBe("true");
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "down" } });
  expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Open Payment API Down \u00b7 https://payments.example"]);
  expect(groupNames()).toEqual(["Monitors"]);
});

it("drops a group whose commands a read-only session does not get", async () => {
  render(<CommandMenu {...props(false)} />);
  await screen.findByRole("option", { name: /Open Payment API/ });
  expect(groupNames()).toEqual(["Monitors", "Navigation", "Theme"]);
});

it("names the keys that drive it, and the close button keeps its name", async () => {
  render(<CommandMenu {...props()} />);
  const keys = [...document.querySelectorAll(".command-keys kbd")].map((k) => k.textContent);
  expect(keys).toEqual(["\u2191", "\u2193", "Enter"]);
  expect(document.querySelector(".command-keys")?.getAttribute("aria-hidden")).toBe("true");
  const close = screen.getByRole("button", { name: "Close command menu" });
  expect(close.getAttribute("aria-keyshortcuts")).toBe("Escape");
  expect(close.querySelector("kbd")?.textContent).toBe("Esc");
});
