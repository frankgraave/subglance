// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from "../App";
import { setToolbarSlot, setTopbarSlot } from "../shell/topbarSlot";

const monitor = (id: number) => ({ id, name: `Service ${id}`, type: "http", target: `https://service-${id}.example`, enabled: true, status: "up", interval_s: 60, timeout_s: 10, tags: {} });
let monitors = Array.from({ length: 200 }, (_, i) => monitor(i + 1));
let role = "admin";
class Source { readyState = 1; addEventListener() {} removeEventListener() {} close() {} }
beforeEach(() => {
  monitors = Array.from({ length: 200 }, (_, i) => monitor(i + 1));
  role = "admin";
  window.history.replaceState(null, "", "/");
  window.localStorage.clear();
  vi.stubGlobal("EventSource", Source);
  vi.stubGlobal("scrollTo", vi.fn());
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    const body = path.endsWith("/auth/me") ? { id: 1, role, email: "operator@example.com" }
      : path === "/api/v1/monitors" ? { monitors }
      : path === "/api/v1/incidents" ? { incidents: [] }
      : path === "/api/v1/channels" ? { channels: [] }
      : path.endsWith("/heartbeats") ? { heartbeats: [] }
      : path.includes("/monitors/") ? monitors.find((m) => String(m.id) === path.split("/")[4])
      : {};
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
});
afterEach(() => { cleanup(); setTopbarSlot(null); setToolbarSlot(null); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

async function launch() {
  await screen.findByText("operator@example.com");
  await act(async () => {});
  fireEvent.keyDown(window, { key: "k", ctrlKey: true });
  return screen.findByRole("dialog", { name: "Command menu" });
}

it("launches globally, searches every monitor and opens the selected result", async () => {
  // Exercise the global palette without also rendering 200 dashboard rows.
  window.history.replaceState(null, "", "/settings");
  render(<App />);
  const dialog = await launch();
  const input = within(dialog).getByRole("combobox");
  await within(dialog).findByRole("option", { name: /Open Service 200/ });
  expect(document.activeElement).toBe(input);
  fireEvent.change(input, { target: { value: "service-200.example" } });
  expect(within(dialog).getAllByRole("option")).toHaveLength(1);
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() => expect(window.location.pathname).toBe("/monitors/200"));
  expect(screen.queryByRole("dialog", { name: "Command menu" })).toBeNull();
});

it("offers each destination, the existing add route and three persisted theme preferences", async () => {
  monitors = [monitor(1)];
  render(<App />);
  let dialog = await launch();
  for (const theme of ["light", "dark", "system"]) {
    fireEvent.click(within(dialog).getByRole("option", { name: `Use ${theme} theme` }));
    expect(localStorage.getItem("subglance:theme")).toBe(theme);
    dialog = await launch();
  }
  for (const [name, path] of [["Monitors", "/monitors"], ["Notifications", "/notifications"], ["Incidents", "/incidents"], ["Settings", "/settings"], ["Dashboard", "/"]]) {
    fireEvent.click(within(dialog).getByRole("option", { name: `Go to ${name}` }));
    await waitFor(() => expect(window.location.pathname).toBe(path));
    dialog = await launch();
  }
  fireEvent.click(within(dialog).getByRole("option", { name: "Add monitor" }));
  await waitFor(() => expect(window.location.pathname).toBe("/monitors/new"));
  await screen.findByRole("dialog", { name: "Add monitor" });
});

it("promotes one global launcher, preserves page search and ignores invalid launch chords", async () => {
  monitors = [monitor(1)];
  render(<App />);
  await screen.findByText("operator@example.com");
  await screen.findByText("Service 1");
  await act(async () => {});
  const input = screen.getByRole("searchbox");
  input.focus();
  fireEvent.change(input, { target: { value: "Service" } });
  for (const extra of [{ isComposing: true }, { keyCode: 229 }, { repeat: true }, { altKey: true }, { shiftKey: true }]) {
    fireEvent.keyDown(input, { key: "k", ctrlKey: true, ...extra });
    expect(screen.queryByRole("dialog", { name: "Command menu" })).toBeNull();
  }
  const launchers = screen.getAllByRole("button", { name: "Open command menu" });
  expect(launchers).toHaveLength(1);
  fireEvent.keyDown(input, { key: "K", metaKey: true });
  const dialog = await screen.findByRole("dialog", { name: "Command menu" });
  fireEvent.keyDown(within(dialog).getByRole("combobox"), { key: "Escape" });
  expect(document.activeElement).toBe(input);
  expect((input as HTMLInputElement).value).toBe("Service");
  fireEvent.click(launchers[0]);
  await screen.findByRole("dialog", { name: "Command menu" });
});
