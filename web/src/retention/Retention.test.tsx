// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { RetentionCard } from "./Retention";
import { formatBytes } from "./format";
import { defaultRetention } from "./fixtures";
import type { Retention } from "./api";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

function json(body: unknown, status = 200, etag?: string) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...(etag ? { etag } : {}) } });
}

/** The retention read as the server sends it: with the version of the windows. */
const settings = (body: unknown, etag = 'W/"4"') => json(body, 200, etag);

function mount(state: Retention, canAdmin = true, onRequest?: (url: string, init?: RequestInit) => Response | undefined) {
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) =>
    onRequest?.(url, init) ?? (url.includes("/preview")
      ? json({ heartbeats: 1234, hourly_buckets: 0, incidents: 0 })
      : settings(state)));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><RetentionCard canAdmin={canAdmin} /></QueryClientProvider>);
  return fetcher;
}

it("shows measured table sizes and the windows in force", async () => {
  mount(defaultRetention);
  expect(await screen.findByText("Raw heartbeats")).toBeTruthy();
  expect(screen.getByText("5.4 MB")).toBeTruthy();
  const raw = screen.getByLabelText("Keep raw heartbeats, in days") as HTMLInputElement;
  expect(raw.value).toBe("30");
  const forever = screen.getAllByLabelText("Forever") as HTMLInputElement[];
  expect(forever[0].checked).toBe(false);
  expect(forever[1].checked).toBe(true);
  // A forever window has no steady state, so it is stated as a yearly rate.
  expect(screen.getByText(/Grows about .* a year at today's rate\./)).toBeTruthy();
});

it("says what a shorter window removes before it is saved", async () => {
  const fetcher = mount(defaultRetention);
  const raw = await screen.findByLabelText("Keep raw heartbeats, in days");
  fireEvent.change(raw, { target: { value: "7" } });
  expect(await screen.findByText(/fold 1,234 raw heartbeats into hourly summaries/)).toBeTruthy();
  expect(fetcher.mock.calls.some(([url]) => String(url).includes("raw_seconds=604800"))).toBe(true);
});

it("does not save a shorter window until its preview has answered", async () => {
  let answer: (response: Response) => void = () => {};
  mount(defaultRetention);
  vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string) => url.includes("/preview")
    ? new Promise<Response>((resolve) => { answer = resolve; })
    : Promise.resolve(json(defaultRetention))));
  fireEvent.change(await screen.findByLabelText("Keep raw heartbeats, in days"), { target: { value: "7" } });
  expect(await screen.findByText("Counting what this change removes…")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save retention" }).matches(":disabled")).toBe(true);
  answer(json({ error: "unavailable" }, 500));
  expect(await screen.findByText("Could not count what this change removes.")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save retention" }).matches(":disabled")).toBe(true);
});

it("waits for a fresh count when returning to a draft it previewed earlier", async () => {
  let hold = false;
  let answer: (response: Response) => void = () => {};
  mount(defaultRetention);
  vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string) => {
    if (!url.includes("/preview")) return Promise.resolve(json(defaultRetention));
    if (hold) return new Promise<Response>((resolve) => { answer = resolve; });
    return Promise.resolve(json({ heartbeats: 1234, hourly_buckets: 0, incidents: 0 }));
  }));
  const raw = await screen.findByLabelText("Keep raw heartbeats, in days");
  const save = () => screen.getByRole("button", { name: "Save retention" });
  fireEvent.change(raw, { target: { value: "7" } });
  await screen.findByText(/fold 1,234 raw heartbeats/);
  fireEvent.change(raw, { target: { value: "8" } });
  await waitFor(() => expect(save().matches(":disabled")).toBe(false));
  hold = true;
  fireEvent.change(raw, { target: { value: "7" } });
  expect(await screen.findByText("Counting what this change removes…")).toBeTruthy();
  expect(save().matches(":disabled")).toBe(true);
  answer(json({ heartbeats: 99, hourly_buckets: 0, incidents: 0 }));
  expect(await screen.findByText(/fold 99 raw heartbeats/)).toBeTruthy();
  expect(save().matches(":disabled")).toBe(false);
});

it("reports a save the server could not read back as saved", async () => {
  mount(defaultRetention, true, (_url, init) => init?.method === "PUT" ? new Response(null, { status: 204 }) : undefined);
  fireEvent.change(await screen.findByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect(await screen.findByText(/Saved\. The new settings apply from the next daily pass\./)).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("does not ask what a longer window removes", async () => {
  const fetcher = mount(defaultRetention);
  fireEvent.change(await screen.findByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  await screen.findByRole("button", { name: "Save retention" });
  expect(fetcher.mock.calls.some(([url]) => String(url).includes("/preview"))).toBe(false);
});

it("saves only the windows that are not pinned", async () => {
  const pinned: Retention = { ...defaultRetention, rollup: { seconds: 365 * 86_400, source: "pinned", pinned_by: "--rollup-retention", set_aside_seconds: null } };
  let sent: unknown;
  mount(pinned, true, (_url, init) => {
    if (init?.method === "PUT") { sent = JSON.parse(String(init.body)); return json({ ...pinned, raw: { ...pinned.raw, seconds: 60 * 86_400, source: "database" } }); }
    return undefined;
  });
  expect(await screen.findByText(/Set by --rollup-retention to 365 days; change it there\./)).toBeTruthy();
  expect(screen.getByLabelText("Keep hourly summaries and resolved incidents, in days").matches(":disabled")).toBe(true);
  fireEvent.change(screen.getByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect(await screen.findByText(/Saved\. The new settings apply from the next daily pass\./)).toBeTruthy();
  expect(sent).toEqual({ raw_seconds: 60 * 86_400, run_at: "03:30", max_database_bytes: 0 });
});

it("places a refusal under the window the server blamed", async () => {
  mount(defaultRetention, true, (_url, init) =>
    init?.method === "PUT" ? json({ error: "hourly summaries must be kept at least as long as raw heartbeats", field: "rollup_seconds" }, 400) : undefined);
  const forever = await screen.findAllByLabelText("Forever");
  fireEvent.click(forever[1]);
  fireEvent.change(screen.getByLabelText("Keep hourly summaries and resolved incidents, in days"), { target: { value: "7" } });
  // A shorter window is saved only once its preview has answered.
  await screen.findByText(/fold 1,234 raw heartbeats/);
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toMatch(/at least as long as raw heartbeats/);
  await waitFor(() => expect(screen.getByLabelText("Keep hourly summaries and resolved incidents, in days").getAttribute("aria-invalid")).toBe("true"));
});

it("is read-only for anyone but an administrator", async () => {
  mount(defaultRetention, false);
  expect(await screen.findByText("Only an administrator can change retention.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Save retention" })).toBeNull();
  expect(screen.getByLabelText("Keep raw heartbeats, in days").matches(":disabled")).toBe(true);
});

it.each([{}, { ...defaultRetention, raw: { seconds: "forever" } }, { ...defaultRetention, tables: [{ name: "users", rows: 1, bytes: 1, rows_per_day: 0 }] },
  { ...defaultRetention, max_database_size: { ...defaultRetention.max_database_size, bytes: "2GB" } }, { ...defaultRetention, run_at: { value: "3:30", source: "default", pinned_by: null } }])(
  "refuses a response it cannot read rather than guessing a window: %j", async (body) => {
    mount(body as Retention);
    expect(await screen.findByText("Retention settings unavailable.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save retention" })).toBeNull();
  });

it("formats sizes in decimal units", () => {
  expect(formatBytes(512)).toBe("512 B");
  expect(formatBytes(5_400_000)).toBe("5.4 MB");
  expect(formatBytes(160_000_000)).toBe("160 MB");
});

it("makes the save conditional on the version it read", async () => {
  let ifMatch: string | null = null;
  mount(defaultRetention, true, (_url, init) => {
    if (init?.method !== "PUT") return undefined;
    ifMatch = new Headers(init.headers).get("If-Match");
    return settings({ ...defaultRetention, raw: { ...defaultRetention.raw, seconds: 60 * 86_400, source: "database" } }, 'W/"5"');
  });
  fireEvent.change(await screen.findByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect(await screen.findByText(/Saved\./)).toBeTruthy();
  expect(ifMatch).toBe('W/"4"');
});

// Read at 30 days, another administrator saves 90. Sixty now shortens
// retention, and that was never previewed, so the draft is not retried: the
// form restarts from what is in force and says why.
it("reloads the windows instead of retrying when someone else saved first", async () => {
  let current: Retention = defaultRetention;
  let puts = 0;
  mount(defaultRetention, true, (url, init) => {
    if (init?.method === "PUT") {
      puts++;
      current = { ...defaultRetention, raw: { ...defaultRetention.raw, seconds: 90 * 86_400, source: "database" } };
      return json({ error: "retention was changed by someone else since you read it" }, 412);
    }
    return url.includes("/preview") ? undefined : settings(current, 'W/"5"');
  });
  const raw = await screen.findByLabelText("Keep raw heartbeats, in days") as HTMLInputElement;
  fireEvent.change(raw, { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/changed by someone else while you were editing/);
  await waitFor(() => expect((screen.getByLabelText("Keep raw heartbeats, in days") as HTMLInputElement).value).toBe("90"));
  expect(screen.getByRole("button", { name: "Save retention" }).matches(":disabled")).toBe(true);
  expect(puts).toBe(1);
});

it("does not fall back to an unconditional save when the read carried no version", async () => {
  let puts = 0;
  mount(defaultRetention, true, (url, init) => {
    if (init?.method === "PUT") { puts++; return settings(defaultRetention); }
    return url.includes("/preview") ? undefined : json(defaultRetention);
  });
  fireEvent.change(await screen.findByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/Reload the page before saving/);
  expect(puts).toBe(0);
});

const GB = 1_000_000_000;
const MiB = 1_048_576;

it("saves a size limit and a time of day with the windows, under one version", async () => {
  let sent: Record<string, unknown> = {};
  mount(defaultRetention, true, (_url, init) => {
    if (init?.method !== "PUT") return undefined;
    sent = JSON.parse(String(init.body)) as Record<string, unknown>;
    return settings({ ...defaultRetention, run_at: { value: "04:15", source: "database", pinned_by: null },
      max_database_size: { ...defaultRetention.max_database_size, bytes: 500_000_000, source: "database" } }, 'W/"5"');
  });
  const off = await screen.findByLabelText("No limit") as HTMLInputElement;
  expect(off.checked).toBe(true);
  fireEvent.click(off);
  // Switched on, it offers twice what the data takes, so it removes nothing yet.
  const size = screen.getByLabelText("Limit the database to, in MB") as HTMLInputElement;
  expect(size.value).toBe("100");
  expect(screen.getByText(/The data takes 6\.4 MB today, so this removes nothing yet/)).toBeTruthy();
  fireEvent.change(size, { target: { value: "500" } });
  fireEvent.change(screen.getByLabelText("Run the daily pass at"), { target: { value: "04:15" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect(await screen.findByText(/Saved\./)).toBeTruthy();
  expect(sent).toEqual({ raw_seconds: 30 * 86_400, rollup_seconds: 0, run_at: "04:15", max_database_bytes: 500_000_000 });
});

it("refuses a size limit under the server's minimum before asking", async () => {
  mount({ ...defaultRetention, max_database_size: { ...defaultRetention.max_database_size, bytes: GB, source: "database" } });
  const size = await screen.findByLabelText("Limit the database to, in MB");
  fireEvent.change(size, { target: { value: "20" } });
  expect(screen.getByText("At least 34 MB, or no limit.")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save retention" }).matches(":disabled")).toBe(true);
});

it("says a limit under today's data removes history and cannot be counted in advance", async () => {
  const big = { ...defaultRetention, compact: { ...defaultRetention.compact!, size_bytes: 3 * GB, free_bytes: 0 } };
  mount(big);
  fireEvent.click(await screen.findByLabelText("No limit"));
  fireEvent.change(screen.getByLabelText("Limit the database to, in MB"), { target: { value: "2000" } });
  expect(screen.getByText(/The data takes 3\.0 GB today, so the next daily pass removes history beyond the windows.*cannot be counted in advance/)).toBeTruthy();
});

it("keeps a limit set in MiB exact when the rounded box is left alone", async () => {
  const exact = { ...defaultRetention, max_database_size: { ...defaultRetention.max_database_size, bytes: 2048 * MiB, source: "database" as const } };
  let sent: Record<string, unknown> = {};
  mount(exact, true, (_url, init) => {
    if (init?.method !== "PUT") return undefined;
    sent = JSON.parse(String(init.body)) as Record<string, unknown>;
    return settings(exact, 'W/"5"');
  });
  expect((await screen.findByLabelText("Limit the database to, in MB") as HTMLInputElement).value).toBe("2147");
  expect(screen.getByRole("button", { name: "Save retention" }).matches(":disabled")).toBe(true);
  fireEvent.change(screen.getByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  await screen.findByText(/Saved\./);
  expect(sent.max_database_bytes).toBe(2048 * MiB);
});

it("leaves a limit and a time pinned by a flag out of the save", async () => {
  const pinned: Retention = { ...defaultRetention,
    run_at: { value: "02:00", source: "pinned", pinned_by: "--retention-run-at" },
    max_database_size: { ...defaultRetention.max_database_size, bytes: 2 * GB, source: "pinned", pinned_by: "SUBGLANCE_MAX_DATABASE_SIZE" } };
  let sent: unknown;
  mount(pinned, true, (_url, init) => {
    if (init?.method !== "PUT") return undefined;
    sent = JSON.parse(String(init.body));
    return settings(pinned, 'W/"5"');
  });
  expect(await screen.findByText("Set by SUBGLANCE_MAX_DATABASE_SIZE to 2.0 GB; change it there.")).toBeTruthy();
  expect(screen.getByText("Set by --retention-run-at to 02:00; change it there.")).toBeTruthy();
  expect(screen.getByLabelText("Run the daily pass at").matches(":disabled")).toBe(true);
  expect(screen.getByLabelText("Limit the database to, in MB").matches(":disabled")).toBe(true);
  fireEvent.change(screen.getByLabelText("Keep raw heartbeats, in days"), { target: { value: "60" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  await screen.findByText(/Saved\./);
  expect(sent).toEqual({ raw_seconds: 60 * 86_400, rollup_seconds: 0 });
});

it("places a refused time of day under its field", async () => {
  mount(defaultRetention, true, (_url, init) =>
    init?.method === "PUT" ? json({ error: "run_at must be a time of day such as 03:30", field: "run_at" }, 400) : undefined);
  fireEvent.change(await screen.findByLabelText("Run the daily pass at"), { target: { value: "05:00" } });
  fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/time of day such as 03:30/);
  expect(screen.getByLabelText("Run the daily pass at").getAttribute("aria-invalid")).toBe("true");
});

it("shows what the last pass did", async () => {
  mount(defaultRetention);
  expect(await screen.findByText(/Folded 14,400 raw heartbeats into hourly summaries and deleted 0 hourly summaries, 0 resolved incidents and 3 delivered notifications\./)).toBeTruthy();
  expect(screen.getByText("At its time of day")).toBeTruthy();
  expect(screen.getByText("1.1 MB")).toBeTruthy();
  expect(screen.getByText("1.8 s")).toBeTruthy();
});

it("says when the size limit shortened raw data, and when it could not get under", async () => {
  const capped: Retention = { ...defaultRetention, max_database_size: { ...defaultRetention.max_database_size, bytes: 2 * GB, source: "database" },
    last_pass: { ...defaultRetention.last_pass!, size_cap: {
      limit_bytes: 2 * GB, before_bytes: 2.4 * GB, after_bytes: 2.1 * GB, heartbeats: 900_000, hourly_buckets: 0,
      raw_since: "2026-09-18T03:30:00Z", hourly_since: null, at_floor: true } } };
  mount(capped);
  expect(await screen.findByText(/Shortened to 12 days of raw data to stay under 2\.0 GB\./)).toBeTruthy();
  expect(screen.getByText(/Still over 2\.0 GB \(2\.1 GB\): only the last day and the incidents are left/)).toBeTruthy();
});

it("shows a failed pass as an error", async () => {
  mount({ ...defaultRetention, last_pass: { ...defaultRetention.last_pass!, error: "database or disk is full" } });
  expect(await screen.findByText("The last pass failed: database or disk is full")).toBeTruthy();
});

it("keeps the settings usable when a report cannot be read", async () => {
  mount({ ...defaultRetention, last_pass: { started_at: 12 } as unknown as Retention["last_pass"], compact: { size_bytes: "big" } as unknown as Retention["compact"] });
  expect(await screen.findByText(/No pass recorded yet\. The next runs at 03:30, or when started here\./)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save retention" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Compact database" })).toBeNull();
});
it("leaves out a report that lacks a field the card reads", async () => {
  const pass = defaultRetention.last_pass!;
  const cap = { limit_bytes: 2 * GB, before_bytes: 2.4 * GB, after_bytes: 2.1 * GB, heartbeats: 900_000, hourly_buckets: 0,
    raw_since: null, hourly_since: null, at_floor: true };
  // Recommended, so a plan the page accepted would show its panel and button.
  const plan = { ...defaultRetention.compact!, auto_vacuum: "none", recommended: true };
  const last = { finished_at: "2026-09-30T10:00:00Z", duration_ms: 12_000, before_bytes: 400 * MiB, after_bytes: 280 * MiB,
    shrink_pending: false, error: null };
  const caps = [{ ...cap, after_bytes: undefined }, { ...cap, before_bytes: -1 }, { ...cap, at_floor: "yes" }];
  const plans = [{ ...plan, auto_vacuum: "sometimes" }, { ...plan, disk_shortfall: {} }, { ...plan, disk_shortfall: { need_bytes: 800 * MiB } },
    { ...plan, last: { ...last, duration_ms: undefined } }, { ...plan, last: { ...last, shrink_pending: undefined } }];
  for (const size_cap of caps) {
    mount({ ...defaultRetention, last_pass: { ...pass, size_cap } } as unknown as Retention);
    expect(await screen.findByText(/No pass recorded yet\./)).toBeTruthy();
    cleanup();
  }
  for (const compact of plans) {
    mount({ ...defaultRetention, compact } as unknown as Retention);
    await screen.findByRole("button", { name: "Save retention" });
    expect(screen.queryByText("Database file")).toBeNull();
    expect(screen.queryByRole("button", { name: "Compact database" })).toBeNull();
    cleanup();
  }
});

it("counts what a pass would remove before starting one by hand", async () => {
  const posts: string[] = [];
  mount({ ...defaultRetention, max_database_size: { ...defaultRetention.max_database_size, bytes: 2 * GB, source: "database" } }, true, (url, init) => {
    if (init?.method !== "POST") return undefined;
    posts.push(url);
    return json({ running: true }, 202);
  });
  fireEvent.click(await screen.findByRole("button", { name: "Run now…" }));
  expect(await screen.findByText(/A pass now would fold 1,234 raw heartbeats into hourly summaries/)).toBeTruthy();
  expect(screen.getByText(/2\.0 GB size limit may remove more.*cannot be counted in advance/)).toBeTruthy();
  expect(posts).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Start the pass" }));
  await waitFor(() => expect(posts).toEqual(["/api/v1/settings/retention/run"]));
  expect(await screen.findByRole("button", { name: "Run now…" })).toBeTruthy();
});

it("does not start a pass until the count has answered", async () => {
  let answer: (response: Response) => void = () => {};
  mount(defaultRetention);
  vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string) => url.includes("/preview")
    ? new Promise<Response>((resolve) => { answer = resolve; })
    : Promise.resolve(settings(defaultRetention))));
  fireEvent.click(await screen.findByRole("button", { name: "Run now…" }));
  expect(await screen.findByText("Counting what a pass would remove now…")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Start the pass" }).matches(":disabled")).toBe(true);
  answer(json({ error: "unavailable" }, 500));
  expect(await screen.findByText(/Could not count what a pass would remove\. It can still be started\./)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Start the pass" }).matches(":disabled")).toBe(false);
});

it("says why a pass could not be started", async () => {
  mount(defaultRetention, true, (_url, init) =>
    init?.method === "POST" ? json({ error: "a retention pass is already running; its result will appear when it finishes" }, 409) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Run now…" }));
  await screen.findByText(/A pass now would fold/);
  fireEvent.click(screen.getByRole("button", { name: "Start the pass" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/already running/);
});

it("follows a running pass instead of offering another", async () => {
  mount({ ...defaultRetention, running: true });
  expect(await screen.findByText("A pass is running. Its outcome appears here when it finishes.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Run now…" })).toBeNull();
});

it("offers no maintenance actions to anyone but an administrator", async () => {
  mount({ ...defaultRetention, compact: { ...defaultRetention.compact!, auto_vacuum: "none", recommended: true } }, false);
  expect(await screen.findByText(/never shrinks by itself/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Run now…" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Compact database" })).toBeNull();
});

it("offers compaction only when it would help", async () => {
  mount(defaultRetention);
  await screen.findByText(/Folded 14,400/);
  expect(screen.queryByRole("button", { name: "Compact database" })).toBeNull();
  expect(screen.queryByText("Database file")).toBeNull();
});

it("says what compacting costs, then starts it", async () => {
  const posts: string[] = [];
  const plan = { ...defaultRetention.compact!, size_bytes: 400 * MiB, free_bytes: 120 * MiB, auto_vacuum: "none" as const, recommended: true, estimate_seconds: 25 };
  mount({ ...defaultRetention, compact: plan }, true, (url, init) => {
    if (init?.method !== "POST") return undefined;
    posts.push(url);
    return json({ running: true, estimate_seconds: 25 }, 202);
  });
  expect(await screen.findByText(/The 419 MB file never shrinks by itself \(126 MB of it is empty now\)/)).toBeTruthy();
  expect(screen.getByText(/takes about 25 seconds, results wait and are recorded when it finishes/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Compact database" }));
  await waitFor(() => expect(posts).toEqual(["/api/v1/settings/retention/compact"]));
});

it("does not offer to compact onto a disk that cannot hold the copy", async () => {
  const plan = { ...defaultRetention.compact!, auto_vacuum: "none" as const, recommended: true,
    disk_shortfall: { need_bytes: 800 * MiB, free_bytes: 300 * MiB } };
  mount({ ...defaultRetention, compact: plan });
  expect(await screen.findByText(/It needs 839 MB free for the copy and the disk has 315 MB, so it cannot run\./)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Compact database" }).matches(":disabled")).toBe(true);
});

it("reports the last compaction and a compaction in progress", async () => {
  const done = { ...defaultRetention.compact!, last: { finished_at: "2026-09-30T10:00:00Z", duration_ms: 12_000, before_bytes: 400 * MiB, after_bytes: 280 * MiB, shrink_pending: true, error: null } };
  mount({ ...defaultRetention, compact: done });
  expect(await screen.findByText(/in 12 s: 419 MB to 294 MB\. The file reaches that size at the next checkpoint\./)).toBeTruthy();
  cleanup();
  mount({ ...defaultRetention, compact: { ...done, running: true, last: null } });
  expect(await screen.findByText(/Compacting the database\. Checks keep running/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Compact database" })).toBeNull();
});
