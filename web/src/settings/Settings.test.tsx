// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ShellSlots } from "../shell/ShellSlots";
import { setToolbarSlot, setTopbarSlot } from "../shell/topbarSlot";
import { Settings } from "./Settings";

const clients: QueryClient[] = [];
function renderSettings() {
  // Every card fetches; an empty 404 keeps them in their unavailable state,
  // which is enough for a test about the page's structure.
  vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => new Response("{}", { status: 404 })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><ShellSlots /><Settings client={client} /></QueryClientProvider>);
}
const index = () => screen.getByRole("navigation", { name: "Settings sections" });
const current = () => within(index()).getAllByRole("link").filter((link) => link.getAttribute("aria-current") === "true").map((link) => link.textContent);

let scrolled: string[];
beforeEach(() => {
  scrolled = [];
  // jsdom does not lay out, so it has no scrollIntoView; record the target.
  Element.prototype.scrollIntoView = function (this: Element) { scrolled.push(this.id); };
});
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  setTopbarSlot(null); setToolbarSlot(null);
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("indexes every section with a link to its anchor, in page order", () => {
  window.history.replaceState(null, "", "/settings");
  renderSettings();
  const links = within(index()).getAllByRole("link");
  expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
    ["Account", "#account"],
    ["Self-monitoring", "#self-monitoring"],
    ["Retention & storage", "#retention"],
    ["API tokens", "#tokens"],
  ]);
  for (const link of links) expect(document.getElementById(link.getAttribute("href")!.slice(1))).toBeTruthy();
  expect(current()).toEqual(["Account"]);
  // "true", never "page": the sidebar owns the page-level claim.
  expect(index().querySelector('[aria-current="page"]')).toBeNull();
});

it("lands a deep link on its section and marks it current", () => {
  window.history.replaceState(null, "", "/settings#tokens");
  renderSettings();
  expect(scrolled).toEqual(["tokens"]);
  expect(current()).toEqual(["API tokens"]);
});

it("ignores a fragment that names no section, or is malformed", () => {
  window.history.replaceState(null, "", "/settings#%E0%A4%A");
  renderSettings();
  expect(current()).toEqual(["Account"]);
  expect(scrolled).toEqual([]);
});

it("follows the address when a link or Back changes the fragment", () => {
  window.history.replaceState(null, "", "/settings");
  renderSettings();
  fireEvent.click(within(index()).getByRole("link", { name: "Retention & storage" }));
  expect(current()).toEqual(["Retention & storage"]);
  act(() => {
    window.history.replaceState(null, "", "/settings#self-monitoring");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  expect(current()).toEqual(["Self-monitoring"]);
});

it("keeps a section chosen by the address until the reader scrolls by hand", () => {
  // jsdom has no layout: every section measures at the top, so any
  // re-derivation from the scroll position picks the last one. That makes a
  // scroll that is wrongly acted on visible here.
  window.history.replaceState(null, "", "/settings#self-monitoring");
  renderSettings();
  act(() => { window.dispatchEvent(new Event("scroll")); });
  expect(current()).toEqual(["Self-monitoring"]);
  act(() => {
    window.dispatchEvent(new WheelEvent("wheel"));
    window.dispatchEvent(new Event("scroll"));
  });
  expect(current()).toEqual(["API tokens"]);
});

it("lists only the sections the search leaves visible, and hides the index when none match", () => {
  window.history.replaceState(null, "", "/settings");
  renderSettings();
  const search = screen.getByRole("searchbox", { name: "Search settings" });
  fireEvent.change(search, { target: { value: "bearer" } });
  expect(within(index()).getAllByRole("link").map((link) => link.textContent)).toEqual(["API tokens"]);
  fireEvent.change(search, { target: { value: "no such setting" } });
  expect(screen.queryByRole("navigation", { name: "Settings sections" })).toBeNull();
  expect(screen.getByRole("status").textContent).toMatch(/No settings match/);
});

it("moves the current section off one the search hides, and does not bring it back", () => {
  window.history.replaceState(null, "", "/settings");
  renderSettings();
  fireEvent.click(within(index()).getByRole("link", { name: "Retention & storage" }));
  expect(current()).toEqual(["Retention & storage"]);
  const search = screen.getByRole("searchbox", { name: "Search settings" });
  fireEvent.change(search, { target: { value: "bearer" } });
  expect(current()).toEqual(["API tokens"]);
  fireEvent.change(search, { target: { value: "no such setting" } });
  fireEvent.change(search, { target: { value: "" } });
  expect(current()).toHaveLength(1);
  expect(current()).not.toEqual(["Retention & storage"]);
});
