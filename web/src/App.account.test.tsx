// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from "./App";
import { LAYOUT_STORAGE_KEY } from "./shell/preferences";

const user = { id: 1, email: "operator@example.test", role: "viewer", created_at: "2026-09-01T00:00:00Z" };
let role = "admin";
let createStatus = 201;
let created = false;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
class Source { addEventListener() {} removeEventListener() {} close() {} }
beforeEach(() => {
  role = "admin"; created = false; createStatus = 201;
  window.history.replaceState(null, "", "/"); window.localStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(window, "confirm").mockReturnValue(false);
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("EventSource", Source);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/auth/me") return json({ ...user, role });
    if (path === "/api/v1/setup") return json({ setup_required: false });
    if (path === "/api/v1/auth/password" || path === "/api/v1/auth/logout") return new Response(null, { status: 204 });
    if (path === "/api/v1/monitors" && init?.method === "POST") {
      if (createStatus !== 201) return json({ error: "save refused" }, createStatus);
      created = true; return json({ id: 9 }, 201);
    }
    if (path.startsWith("/api/v1/monitors")) return json({ monitors: created ? [{
      id: 9, name: "Saved service", type: "http", target: "https://example.test", interval_s: 60, timeout_s: 10,
      enabled: true, status: "up", created_at: user.created_at,
    }] : [] });
    if (path.startsWith("/api/v1/channels")) return json({ channels: [] });
    if (path.startsWith("/api/v1/incidents")) return json({ incidents: [] });
    throw new Error(`Unexpected request: ${path}`);
  }));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function openFromDashboard() {
  render(<App />);
  fireEvent.click(await screen.findByRole("link", { name: "Monitors" }));
  fireEvent.click(await screen.findByRole("button", { name: /add monitor/i }));
  return await screen.findByLabelText("Name");
}
it("opens the password card from the sidebar for a viewer and keeps settings out of wall mode", async () => {
  role = "viewer";
  window.history.replaceState(null, "", "/settings");
  window.localStorage.setItem(LAYOUT_STORAGE_KEY, "wall");
  render(<App />);
  const input = await screen.findByLabelText("Current password");
  expect(screen.getByRole("link", { name: "Settings" }).getAttribute("aria-current")).toBe("page");
  expect(document.title).toMatch(/Settings/);
  expect(document.querySelector(".shell-topbar")).toBeTruthy();
  expect(screen.getAllByRole("region")).toHaveLength(1);
  fireEvent.change(input, { target: { value: "private" } });
  fireEvent.change(screen.getByRole("searchbox", { name: "Search settings" }), { target: { value: "nonexistent" } });
  expect(screen.getByRole("status").textContent).toMatch(/No settings match/);
  fireEvent.change(screen.getByRole("searchbox", { name: "Search settings" }), { target: { value: "password" } });
  expect((screen.getByLabelText("Current password") as HTMLInputElement).value).toBe("private");
});
it.each(["sidebar", "Escape", "field Escape", "workbench", "signout"])("guards %s from /monitors/new reached via the dashboard, preserving the URL and input on cancel", async (path) => {
  const input = await openFromDashboard();
  fireEvent.change(input, { target: { value: "kept draft" } });
  const dismiss = () => {
    if (path === "sidebar") fireEvent.click(screen.getByRole("link", { name: "Settings" }));
    if (path === "signout") fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    if (path === "Escape") fireEvent.keyDown(window, { key: "Escape" });
    if (path === "field Escape") fireEvent.keyDown(input, { key: "Escape" });
    if (path === "workbench") fireEvent.click(screen.getByRole("button", { name: "Component workbench" }));
  };
  dismiss();
  expect(window.confirm).toHaveBeenCalledTimes(1);
  expect(window.location.pathname).toBe("/monitors/new");
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("kept draft");
  vi.mocked(window.confirm).mockReturnValue(true); dismiss();
  expect(window.confirm).toHaveBeenCalledTimes(2);
  expect(screen.queryByLabelText("Name")).toBeNull();
});
it("does not treat a same-route hash change as discarding the dirty form", async () => {
  const input = await openFromDashboard();
  fireEvent.change(input, { target: { value: "still mounted" } });
  window.history.pushState(window.history.state, "", "/monitors/new#help");
  fireEvent.popState(window);
  expect(window.confirm).not.toHaveBeenCalled();
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("still mounted");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(window.confirm).toHaveBeenCalledTimes(1);
});

it("preserves both the form and browser history when Back is cancelled, then allows Back and Forward", async () => {
  const input = await openFromDashboard();
  fireEvent.change(input, { target: { value: "history draft" } });
  window.history.back();
  await waitFor(() => expect(window.confirm).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(window.location.pathname).toBe("/monitors/new"));
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("history draft");
  vi.mocked(window.confirm).mockReturnValue(true);
  window.history.back();
  await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
  await waitFor(() => expect(screen.queryByLabelText("Name")).toBeNull());
  window.history.forward();
  await waitFor(() => expect(window.location.pathname).toBe("/monitors/new"));
  expect((await screen.findByLabelText("Name") as HTMLInputElement).value).toBe("");
});

it("guards the mounted draft even when the browser URL has already moved to the Cancel destination", async () => {
  const input = await openFromDashboard();
  fireEvent.change(input, { target: { value: "mounted draft" } });
  // A traversal updates location before the router processes its popstate.
  window.history.replaceState(window.history.state, "", "/monitors");
  fireEvent.keyDown(window, { key: "Escape" });
  expect(window.confirm).toHaveBeenCalledTimes(1);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("mounted draft");
});

it("verifies the restoration target instead of swallowing an intervening Back", async () => {
  const input = await openFromDashboard();
  fireEvent.change(input, { target: { value: "racing draft" } });
  const go = window.history.go.bind(window.history);
  // Hold the router's reversal while another browser traversal wins the race.
  const reversal = vi.spyOn(window.history, "go").mockImplementation(() => {});
  go(-1);
  await waitFor(() => expect(reversal).toHaveBeenCalledWith(1));
  go(-1);
  await waitFor(() => expect(window.location.pathname).toBe("/"));
  await waitFor(() => expect(reversal).toHaveBeenLastCalledWith(2));
  expect(window.confirm).toHaveBeenCalledTimes(1);
  go(2);
  await waitFor(() => expect(window.location.pathname).toBe("/monitors/new"));
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("racing draft");
  fireEvent.keyDown(window, { key: "Escape" });
  expect(window.confirm).toHaveBeenCalledTimes(2);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("racing draft");
});

it("does not prompt after a real create succeeds, reopens pristine, but keeps a failed save dirty", async () => {
  await openFromDashboard();
  const fill = () => {
    fireEvent.change(screen.getByLabelText("What should be watched"), { target: { value: "https://example.test" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Saved service" } });
    fireEvent.change(screen.getByLabelText("Check type"), { target: { value: "http" } });
  };
  fill(); fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect(window.location.pathname).toBe("/monitors"));
  expect(window.confirm).not.toHaveBeenCalled();
  await screen.findByText("Saved service");
  fireEvent.click(screen.getByRole("button", { name: /add monitor/i }));
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("");
  fill(); createStatus = 500;
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await screen.findByText(/save refused/);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(window.confirm).toHaveBeenCalledTimes(1);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Saved service");
});
