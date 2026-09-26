// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ShellSlots } from "../shell/ShellSlots";
import { setToolbarSlot, setTopbarSlot } from "../shell/topbarSlot";
import { Settings } from "../settings/Settings";
import { disabledWatchdog } from "../watchdog/fixtures";
import { diagnosticsText, formatBytes, formatUptime } from "./format";
import { steadyDiagnostics } from "./fixtures";
import { diagnosticsKey } from "./api";

const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "diagnostics.css"), "utf8");

const clients: QueryClient[] = [];
function settings(diagnostics: unknown, { canAdmin = true, status = 200 } = {}) {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/api/v1/watchdog") return new Response(JSON.stringify(disabledWatchdog));
    if (path === "/api/v1/diagnostics") return new Response(JSON.stringify(diagnostics), { status });
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><ShellSlots /><Settings client={client} canAdmin={canAdmin} /></QueryClientProvider>);
  return fetcher;
}
afterEach(() => { cleanup(); for (const c of clients.splice(0)) c.clear(); setTopbarSlot(null); setToolbarSlot(null); vi.unstubAllGlobals(); });

const card = () => document.getElementById("instance") as HTMLElement;
const reading = (label: string) => within(card()).getByText(label).nextElementSibling as HTMLElement;

it("shows the pool, the database and the build an administrator asked about", async () => {
  settings(steadyDiagnostics);
  await screen.findByText("2 / 16 busy");
  expect(reading("Version").textContent).toBe("0.1.0 (a17f3c9)");
  expect(reading("Runtime").textContent).toBe("go1.25.1 linux/amd64");
  expect(reading("Path").textContent).toBe("/var/lib/subglance/subglance.db");
  expect(reading("Size").textContent).toBe("24 MiB + 4.0 MiB write-ahead log");
  expect(reading("Journal mode").textContent).toBe("WAL");
  expect(reading("Uptime").textContent).toBe("1 h 0 m");
  for (const label of ["Uptime", "Started"]) expect(reading(label).querySelector(".value"), label).not.toBeNull();
  expect(reading("Monitors scheduled").textContent).toBe("62");
});

it("dims a zero queue and marks a real one without relying on colour", async () => {
  settings(steadyDiagnostics);
  await screen.findByText("2 / 16 busy");
  const calm = reading("Queue depth").querySelector(".value")!;
  expect(calm.getAttribute("data-zero")).toBe("true");
  expect(calm.getAttribute("data-warn")).toBeNull();
  cleanup();

  settings({ ...steadyDiagnostics, scheduler: { ...steadyDiagnostics.scheduler!, queue_depth: 12, skipped_checks: 3, heartbeat_write_failures: 7 } });
  await screen.findByText("2 / 16 busy");
  for (const [label, caveat] of [
    ["Queue depth", "checks are waiting for a free worker"],
    ["Skipped since start", "a check outran its interval; the pool may be behind"],
    ["Failed writes", "heartbeats could not be written; check free disk space"],
  ]) {
    const value = reading(label).querySelector(".value")!;
    expect(value.getAttribute("data-warn"), label).toBe("true");
    expect(within(reading(label)).getByRole("img", { name: caveat })).toBeTruthy();
  }
});

it("says the pipeline is missing instead of reporting an idle pool", async () => {
  settings({ ...steadyDiagnostics, scheduler: null });
  expect(await screen.findByText("No check pipeline is attached to this process.")).toBeTruthy();
  expect(within(card()).queryByText("Queue depth")).toBeNull();
});

it.each([
  null, {}, { ...steadyDiagnostics, scheduler: { ...steadyDiagnostics.scheduler!, busy: "2" } },
  { ...steadyDiagnostics, scheduler: { ...steadyDiagnostics.scheduler!, queue_depth: -1 } },
  { ...steadyDiagnostics, database: { ...steadyDiagnostics.database, bytes: null } },
  { ...steadyDiagnostics, started_at: "yesterday" },
])("refuses a malformed body rather than drawing it as healthy zeros: %s", async (body) => {
  settings(body);
  expect(await within(card()).findByText("Diagnostics unavailable.")).toBeTruthy();
  expect(within(card()).queryByText(/busy/)).toBeNull();
});

it("is neither rendered nor requested for anyone but an administrator", async () => {
  const fetcher = settings(steadyDiagnostics, { canAdmin: false });
  await screen.findByText("Not configured");
  expect(document.getElementById("instance")).toBeNull();
  expect(fetcher.mock.calls.map(([u]) => String(u))).not.toContain("/api/v1/diagnostics");
});

it("is found by searching for the pool", async () => {
  settings(steadyDiagnostics);
  await screen.findByText("2 / 16 busy");
  fireEvent.change(screen.getByRole("searchbox", { name: "Search settings" }), { target: { value: "queue" } });
  expect(card().hidden).toBe(false);
  expect(document.getElementById("account")?.hidden).toBe(true);
});

it("copies a bug-report summary that leaves the database path out", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  settings(steadyDiagnostics);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  fireEvent.click(await screen.findByRole("button", { name: "Copy diagnostics" }));
  expect(await screen.findByText("Copied. The database path is left out.")).toBeTruthy();
  const copied = writeText.mock.calls[0]![0] as string;
  expect(copied).toContain("workers 2/16 busy, queue 0");
  expect(copied).toContain("subglance 0.1.0 (a17f3c9)");
  expect(copied).not.toContain("/var/lib/subglance");
  expect(copied).not.toContain("stale");
  Reflect.deleteProperty(navigator, "clipboard");
});

it("marks a copied summary as stale when the card shows the last readings", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  settings(steadyDiagnostics);
  await screen.findByText("2 / 16 busy");
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 500 })));
  await clients[0]!.refetchQueries({ queryKey: diagnosticsKey });
  await screen.findByText("Diagnostics unavailable. Showing the last readings.");
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  fireEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));
  await screen.findByText("Copied. The database path is left out.");
  expect(writeText.mock.calls[0]![0] as string).toMatch(/^stale: last successful reading at \d{4}-/);
  Reflect.deleteProperty(navigator, "clipboard");
});

it("keeps the clipboard status region in the accessibility tree before it has a message", async () => {
  settings(steadyDiagnostics);
  await screen.findByText("2 / 16 busy");
  const note = within(card()).getAllByRole("status").find((n) => n.classList.contains("diag-note"));
  expect(note).toBeTruthy();
  expect(css).not.toMatch(/\.diag-note:empty\s*\{[^}]*display:\s*none/);
});

it("formats the readings the way an operator reads them", () => {
  expect(formatBytes(512)).toBe("512 B");
  expect(formatBytes(1536)).toBe("1.5 KiB");
  expect(formatBytes(241 * 1024 * 1024)).toBe("241 MiB");
  expect(formatUptime(18 * 86_400 + 4 * 3_600 + 12 * 60)).toBe("18 d 4 h 12 m");
  expect(formatUptime(59)).toBe("0 m");
  expect(diagnosticsText({ ...steadyDiagnostics, commit: "", scheduler: null })).toMatch(/^subglance 0\.1\.0\n[\s\S]*scheduler not attached$/);
});
