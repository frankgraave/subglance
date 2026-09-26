// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ShellSlots } from "../shell/ShellSlots";
import { setToolbarSlot, setTopbarSlot } from "../shell/topbarSlot";
import { Settings } from "../settings/Settings";
import { backupKey } from "./api";
import { unconfiguredBackup } from "./fixtures";

const healthy = {
  configured: true, target: "s3://ops-backups/subglance/",
  last_success_at: "2026-09-26T03:00:02Z", last_object: "subglance-20260926T030000Z.db.gz", last_size_bytes: 4_200_000,
  last_error: null, last_error_at: null, failures: 0,
};

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); setTopbarSlot(null); setToolbarSlot(null); vi.unstubAllGlobals(); });

function mount(body: unknown, { status = 200, canAdmin = true } = {}) {
  // Every other card on the page gets the same body; only the backup card is under test.
  const fetcher = vi.fn().mockImplementation(async () => new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><ShellSlots /><Settings client={client} canAdmin={canAdmin} /></QueryClientProvider>);
  return { client, fetcher };
}

const backupCalls = (fetcher: ReturnType<typeof vi.fn>) => fetcher.mock.calls.filter(([url]) => String(url).includes("/api/v1/backup"));

it("shows the target and the last successful backup", async () => {
  mount(healthy);
  expect(await screen.findByText("s3://ops-backups/subglance/")).toBeTruthy();
  expect(screen.getByText("subglance-20260926T030000Z.db.gz")).toBeTruthy();
  expect(screen.getByText("4.2 MB")).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T03:00:02Z"]')).toBeTruthy();
  expect(screen.queryByText(/failed at/)).toBeNull();
});

it("keeps the last good backup beside a failing streak", async () => {
  mount({ ...healthy, last_error: "upload: 403 AccessDenied", last_error_at: "2026-09-26T04:00:00Z", failures: 3 });
  expect(await screen.findByText(/upload: 403 AccessDenied/)).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T04:00:00Z"]')).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T03:00:02Z"]')).toBeTruthy();
  expect(screen.getByText("3")).toBeTruthy();
});

it("says a configured target has not produced a backup yet", async () => {
  mount({ ...healthy, last_success_at: null, last_object: null, last_size_bytes: null });
  expect(await screen.findByText("Waiting for the first backup since this process started.")).toBeTruthy();
  expect(screen.getByText("None since this process started")).toBeTruthy();
});

it("says plainly when nothing is backed up", async () => {
  mount(unconfiguredBackup);
  expect(await screen.findByText("Not configured. The database is not copied anywhere on a schedule.")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Read about backups" }).getAttribute("href")).toContain("operations.md#scheduled-backups");
});

it.each([
  null, {}, [], { configured: false },
  { ...unconfiguredBackup, target: "s3://leak/" },
  { ...healthy, configured: "true" },
  { ...healthy, target: null },
  { ...healthy, last_size_bytes: -1 },
  { ...healthy, last_object: null },
  { ...healthy, last_error: "boom" },
  { ...healthy, last_success_at: "yesterday" },
  { ...healthy, failures: 1.5 },
])("rejects a malformed body rather than calling it unconfigured: %j", async (body) => {
  mount(body);
  expect(await screen.findByText("Backup state unavailable.")).toBeTruthy();
  expect(screen.queryByText(/Not configured\. The database/)).toBeNull();
});

it("treats a 503 as unknown, not as unconfigured", async () => {
  mount({ error: "backup state unavailable" }, { status: 503 });
  expect(await screen.findByText("Backup state unavailable.")).toBeTruthy();
  expect(screen.queryByText(/Not configured\. The database/)).toBeNull();
});

it("keeps the last retrieved history when a refresh fails", async () => {
  const { client, fetcher } = mount(healthy);
  await screen.findByText("s3://ops-backups/subglance/");
  fetcher.mockImplementation(async () => new Response("{}", { status: 503 }));
  await act(() => client.invalidateQueries({ queryKey: backupKey }));
  expect(await screen.findByText("Backup state unavailable. Showing the last retrieved history.")).toBeTruthy();
  expect(screen.getByText("s3://ops-backups/subglance/")).toBeTruthy();
});

it("is neither shown nor fetched for a non-administrator", async () => {
  const { fetcher } = mount(healthy, { canAdmin: false });
  await screen.findAllByText(/./);
  expect(screen.queryByText("Backups")).toBeNull();
  expect(backupCalls(fetcher)).toHaveLength(0);
});

it("is found by the settings search", async () => {
  mount(healthy);
  await screen.findByText("s3://ops-backups/subglance/");
  fireEvent.change(screen.getByLabelText("Search settings"), { target: { value: "restore" } });
  expect(document.getElementById("backups")?.hidden).toBe(false);
  expect(document.getElementById("account")?.hidden).toBe(true);
  fireEvent.change(screen.getByLabelText("Search settings"), { target: { value: "password" } });
  expect(document.getElementById("backups")?.hidden).toBe(true);
});
