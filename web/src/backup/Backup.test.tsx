// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Settings } from "../settings/Settings";
import { backupKey } from "./api";
import { unconfiguredBackup } from "./fixtures";
import { readFileSync } from "node:fs";

const healthy = {
  configured: true, target: "s3://ops-backups/subglance/",
  last_success_at: "2026-09-26T03:00:02Z", last_object: "subglance-20260926T030000Z.db.gz", last_size_bytes: 4_200_000,
  last_error: null, last_error_at: null, failures: 0,
};

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

function mount(body: unknown, { status = 200, canAdmin = true } = {}) {
  // Every other card on the page gets the same body; only the backup card is under test.
  const fetcher = vi.fn().mockImplementation(async () => new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><Settings client={client} canAdmin={canAdmin} /></QueryClientProvider>);
  return { client, fetcher };
}

const backupCalls = (fetcher: ReturnType<typeof vi.fn>) => fetcher.mock.calls.filter(([url]) => String(url).includes("/api/v1/backup"));

it("shows the target and the last successful backup", async () => {
  mount(healthy);
  expect(await screen.findByText("s3://ops-backups/subglance/")).toBeTruthy();
  expect(screen.getByText("subglance-20260926T030000Z.db.gz")).toBeTruthy();
  expect(screen.getByText("4.2 MB")).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T03:00:02Z"]')).toBeTruthy();
  expect(screen.queryByText(/reported an error/)).toBeNull();
});

it("keeps the last good backup beside a failing streak", async () => {
  mount({ ...healthy, last_error: "upload: 403 AccessDenied", last_error_at: "2026-09-26T04:00:00Z", failures: 3 });
  expect(await screen.findByText(/upload: 403 AccessDenied/)).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T04:00:00Z"]')).toBeTruthy();
  expect(document.querySelector('time[datetime="2026-09-26T03:00:02Z"]')).toBeTruthy();
  expect(screen.getByText("3").classList.contains("face-mono")).toBe(true);
  expect(document.querySelector(`time[datetime="2026-09-26T04:00:00Z"]`)?.closest(".face-mono")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Restoring a backup" }).getAttribute("href")).toContain("operations.md#restoring-from-s3");
});

it("says a configured target has not produced a backup yet", async () => {
  mount({ ...healthy, last_success_at: null, last_object: null, last_size_bytes: null });
  expect(await screen.findByText("Waiting for the first backup since this process started.")).toBeTruthy();
  expect(screen.getByText("None since this process started")).toBeTruthy();
});

it("says plainly when nothing is backed up", async () => {
  mount(unconfiguredBackup);
  expect(await screen.findByText("Not configured. SubGlance has no scheduled backup target.")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Read about backups" }).getAttribute("href")).toContain("operations.md#scheduled-backups");
});

it("names the settings an unset target needs, not only a link", async () => {
  mount(unconfiguredBackup);
  await screen.findByText("Not configured. SubGlance has no scheduled backup target.");
  const card = document.getElementById("backups")!;
  const required = [...card.querySelectorAll(".panel-settings dt")].map((el) => el.textContent);
  expect(required).toEqual(["SUBGLANCE_BACKUP_TARGET", "SUBGLANCE_BACKUP_ACCESS_KEY_ID", "SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE"]);
  // Every variable the card names is a row of the configuration table, so a
  // rename in the configuration cannot leave the card naming a dead setting.
  const named = [...card.querySelectorAll("code")].map((el) => el.textContent ?? "").filter((text) => text.startsWith("SUBGLANCE_"));
  expect(named).toEqual(expect.arrayContaining([...required, "SUBGLANCE_BACKUP_SECRET_ACCESS_KEY", "SUBGLANCE_BACKUP_REGION", "SUBGLANCE_BACKUP_ENDPOINT"]));
  const table = readFileSync("../docs/operations.md", "utf8").split("\n").filter((line) => line.startsWith("|"));
  for (const name of named) expect(table.some((row) => row.includes(`\`${name}\``)), name).toBe(true);
});

it("does not explain the setup once a target is set", async () => {
  mount(healthy);
  await screen.findByText("s3://ops-backups/subglance/");
  expect(document.querySelector("#backups .panel-settings")).toBeNull();
  expect(document.getElementById("backups")!.textContent).not.toContain("SUBGLANCE_");
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
  expect(screen.queryByText(/Not configured\. SubGlance has/)).toBeNull();
});

it("treats a 503 as unknown, not as unconfigured", async () => {
  mount({ error: "backup state unavailable" }, { status: 503 });
  expect(await screen.findByText("Backup state unavailable.")).toBeTruthy();
  expect(screen.queryByText(/Not configured\. SubGlance has/)).toBeNull();
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
  fireEvent.change(screen.getByLabelText("Filter settings"), { target: { value: "restore" } });
  expect(document.getElementById("backups")?.hidden).toBe(false);
  expect(document.getElementById("account")?.hidden).toBe(true);
  fireEvent.change(screen.getByLabelText("Filter settings"), { target: { value: "password" } });
  expect(document.getElementById("backups")?.hidden).toBe(true);
});
