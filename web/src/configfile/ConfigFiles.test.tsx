// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { confirmLeave } from "../shell/leaveGuard";
import { ConfigFilesCard } from "./ConfigFiles";
import { appliedReport, dryRunReport, unchangedReport } from "./fixtures";
import type { ImportReport } from "./api";

const clients: QueryClient[] = [];
beforeEach(() => {
  vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:config"), revokeObjectURL: vi.fn() }));
});
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

type Handler = (url: string, init?: RequestInit) => Response | Promise<Response>;

function mount(handler: Handler) {
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => handler(url, init));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const invalidate = vi.spyOn(client, "invalidateQueries");
  render(<QueryClientProvider client={client}><ConfigFilesCard /></QueryClientProvider>);
  return { fetcher, invalidate };
}

/** An importer that answers the dry run with one report and the apply with another. */
const importer = (dry: ImportReport, applied: ImportReport = { ...dry, dry_run: false }): Handler => (url) =>
  url.endsWith("?dry_run=true") ? json(dry) : json(applied);

const yaml = "version: 1\nmonitors:\n  - key: shop\n    name: Webshop\n";

function choose(text = yaml, name = "subglance.yaml") {
  const file = new File([text], name, { type: "application/yaml" });
  fireEvent.change(screen.getByLabelText("Configuration file"), { target: { files: [file] } });
  return file;
}

const posts = (fetcher: ReturnType<typeof vi.fn>) => fetcher.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === "POST");

it("downloads the export as subglance-config.yaml without leaving the page", async () => {
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
  const downloads: string[] = [];
  click.mockImplementation(function (this: HTMLAnchorElement) { downloads.push(`${this.download} ${this.href}`); });
  const { fetcher } = mount(() => new Response("version: 1\n", { status: 200, headers: { "content-type": "application/yaml" } }));
  fireEvent.click(screen.getByRole("button", { name: "Download configuration" }));
  expect(await screen.findByText("Downloaded as subglance-config.yaml.")).toBeTruthy();
  expect(fetcher).toHaveBeenCalledWith("/api/v1/config/export", expect.objectContaining({ cache: "no-store" }));
  expect(downloads).toEqual(["subglance-config.yaml blob:config"]);
  // The link is not left behind in the document.
  expect(document.querySelector("a[download]")).toBeNull();
});

it("says why an export was refused instead of navigating to the error", async () => {
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
  mount(() => json({ error: "this action needs the editor role" }, 403));
  fireEvent.click(screen.getByRole("button", { name: "Download configuration" }));
  expect((await screen.findByRole("alert")).textContent).toBe("this action needs the editor role");
  expect(click).not.toHaveBeenCalled();
});

it("checks a chosen file with a dry run and lists only what would change", async () => {
  const { fetcher } = mount(importer(dryRunReport));
  choose();
  const report = await screen.findByRole("group", { name: "What this file would change" });
  const [url, init] = posts(fetcher)[0] as [string, RequestInit];
  expect(url).toBe("/api/v1/config/import?dry_run=true");
  expect(init.body).toBe(yaml);
  expect(new Headers(init.headers).get("Content-Type")).toBe("application/yaml");
  expect(within(report).getByText("3 to create, 2 to update, 2 unchanged.")).toBeTruthy();
  const monitors = within(report).getByRole("list", { name: "Monitors" });
  expect(within(monitors).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "Update Webshop shopChanges name, interval_s",
    "Create Nightly backup nightly-backup",
  ]);
  // Unchanged objects are counted, not listed.
  expect(within(report).queryByText("API")).toBeNull();
  expect(within(report).queryByText("Pager")).toBeNull();
  expect(within(report).getByRole("list", { name: "Routing rules" }).textContent).toContain("team=web");
  expect(within(report).queryByRole("list", { name: "Maintenance windows" })).toBeNull();
  // A status page is named with its slug and what changes on it.
  expect(within(report).getByRole("list", { name: "Status pages" }).textContent).toBe("Update Acme services acmeChanges monitors");
  // Nothing is written until the reader confirms.
  expect(posts(fetcher)).toHaveLength(1);
  expect(screen.getByRole("button", { name: "Import subglance.yaml" })).toBeTruthy();
});

it("marks an object that needs a secret, names the field, and says it is saved switched off", async () => {
  mount(importer(dryRunReport));
  choose();
  const report = await screen.findByRole("group", { name: "What this file would change" });
  expect(within(report).getByText(/One object is saved switched off/)).toBeTruthy();
  const channel = within(within(report).getByRole("list", { name: "Channels" })).getByRole("listitem");
  expect(within(channel).getByText("switched off")).toBeTruthy();
  expect(channel.textContent).toContain("this instance has no value for url. Fill it in after the import");
  expect(within(channel).getByText("url").tagName).toBe("CODE");
});

it("applies the checked file only on confirmation, then reports what was imported", async () => {
  const applied: ImportReport = { ...dryRunReport, dry_run: false };
  const { fetcher, invalidate } = mount(importer(dryRunReport, applied));
  choose();
  fireEvent.click(await screen.findByRole("button", { name: "Import subglance.yaml" }));
  expect(await screen.findByRole("group", { name: "What was imported" })).toBeTruthy();
  const [url, init] = posts(fetcher)[1] as [string, RequestInit];
  expect(url).toBe("/api/v1/config/import");
  expect(init.body).toBe(yaml);
  expect(screen.getByText("3 created, 2 updated, 2 unchanged.")).toBeTruthy();
  expect(screen.getByText(/One object is now switched off/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Import / })).toBeNull();
  // Monitors and channels on other screens are stale after an import.
  expect(invalidate).toHaveBeenCalled();
  // The input is cleared, so the same file can be chosen again for a re-check.
  expect((screen.getByLabelText("Configuration file") as HTMLInputElement).files?.length ?? 0).toBe(0);
});

it("places a 400 at its path in the file and offers no import", async () => {
  const { fetcher } = mount(() => json({ error: "interval_s must be between 20 and 86400", field: "monitors[2].interval_s" }, 400));
  choose();
  const alert = await screen.findByRole("alert");
  expect(within(alert).getByText("monitors[2].interval_s").tagName).toBe("CODE");
  expect(alert.textContent).toContain("In the file at monitors[2].interval_s");
  expect(alert.textContent).toContain("interval_s must be between 20 and 86400");
  expect(alert.textContent).toContain("Nothing was imported.");
  expect(screen.queryByRole("button", { name: /^Import / })).toBeNull();
  expect(posts(fetcher)).toHaveLength(1);
});

it("shows a 400 that points nowhere without inventing a path", async () => {
  mount(() => json({ error: "version must be 1" }, 400));
  choose();
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).not.toContain("In the file at");
  expect(alert.textContent).toContain("version must be 1");
});

it("says a failed apply may be partly written, and how to finish it", async () => {
  mount((url) => url.endsWith("?dry_run=true") ? json(dryRunReport) : json({ error: "the database failed after 2 of 4 objects" }, 500));
  choose();
  fireEvent.click(await screen.findByRole("button", { name: "Import subglance.yaml" }));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("the database failed after 2 of 4 objects");
  expect(alert.textContent).toContain("Importing the same file again completes the import");
});

it("shows the push URL of a created push monitor once, then the report", async () => {
  mount(importer(dryRunReport, appliedReport));
  choose();
  fireEvent.click(await screen.findByRole("button", { name: "Import subglance.yaml" }));
  const url = appliedReport.monitors[2].push_url as string;
  expect(await screen.findByDisplayValue(url)).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Nightly backup is waiting for its first report" })).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toMatch(/This URL is shown once/);
  // The report waits behind the reveal, so the URL cannot be scrolled past.
  expect(screen.queryByRole("group", { name: "What was imported" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
  expect(await screen.findByRole("group", { name: "What was imported" })).toBeTruthy();
  expect(screen.queryByDisplayValue(url)).toBeNull();
  expect(document.body.textContent).not.toContain(url);
});

it("shows one reveal per created push monitor, in turn", async () => {
  const two: ImportReport = { ...appliedReport, monitors: [
    { key: "a", name: "Job A", action: "create", push_url: "https://x.test/api/v1/push/aaa" },
    { key: "b", name: "Job B", action: "create", push_url: "https://x.test/api/v1/push/bbb" },
  ] };
  mount(importer(dryRunReport, two));
  choose();
  fireEvent.click(await screen.findByRole("button", { name: "Import subglance.yaml" }));
  expect(await screen.findByDisplayValue("https://x.test/api/v1/push/aaa")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
  expect(await screen.findByDisplayValue("https://x.test/api/v1/push/bbb")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
  expect(await screen.findByRole("group", { name: "What was imported" })).toBeTruthy();
});

it("offers no import when the file already matches the instance", async () => {
  mount(importer(unchangedReport));
  choose();
  expect(await screen.findByText(/already matches this instance/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Import / })).toBeNull();
  expect(screen.getByRole("button", { name: "Choose another file" })).toBeTruthy();
});

it("refuses a file over 4 MiB without uploading it", async () => {
  const { fetcher } = mount(importer(dryRunReport));
  choose("x".repeat((4 << 20) + 1));
  expect((await screen.findByRole("alert")).textContent).toContain("larger than 4 MiB");
  expect(fetcher).not.toHaveBeenCalled();
});

it("treats an unreadable report as an error, never as nothing to do", async () => {
  mount(() => json({ dry_run: true, monitors: "all of them" }));
  choose();
  expect((await screen.findByRole("alert")).textContent).toContain("its report was unreadable. Nothing was imported.");
  expect(screen.queryByText(/already matches/)).toBeNull();
});

it("keeps only the latest file's report when a second is chosen during a check", async () => {
  let release: (response: Response) => void = () => undefined;
  const slow = new Promise<Response>((resolve) => { release = resolve; });
  let first = true;
  mount((url) => {
    if (first) { first = false; return slow; }
    return url.endsWith("?dry_run=true") ? json(unchangedReport) : json({ ...unchangedReport, dry_run: false });
  });
  choose(yaml, "old.yaml");
  // The input is disabled while a check runs; a change event still arrives in a test, as it could from a slow re-render.
  fireEvent.change(screen.getByLabelText("Configuration file"), { target: { files: [new File(["version: 1\n"], "new.yaml")] } });
  expect(await screen.findByText(/already matches this instance/)).toBeTruthy();
  release(json(dryRunReport));
  await waitFor(() => expect(screen.queryByText(/to create/)).toBeNull());
  expect(screen.getByText(/already matches this instance/)).toBeTruthy();
});

it("cancel discards a checked file and writes nothing", async () => {
  const { fetcher } = mount(importer(dryRunReport));
  choose();
  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("group")).toBeNull();
  expect(posts(fetcher)).toHaveLength(1);
});

it("asks before leaving while a push URL is on screen, and not after it is saved", async () => {
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  mount(importer(dryRunReport, appliedReport));
  choose();
  expect(confirmLeave()).toBe(true);
  fireEvent.click(await screen.findByRole("button", { name: "Import subglance.yaml" }));
  await screen.findByRole("button", { name: "I have saved it" });
  expect(confirmLeave()).toBe(false);
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining("unsaved push URL"));
  fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
  await screen.findByRole("group", { name: "What was imported" });
  confirm.mockClear();
  expect(confirmLeave()).toBe(true);
  expect(confirm).not.toHaveBeenCalled();
});
