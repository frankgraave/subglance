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
  expect(await screen.findByText(/Saved\. The new windows apply from the next daily pass\./)).toBeTruthy();
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
  expect(await screen.findByText(/Saved\. The new windows apply from the next daily pass\./)).toBeTruthy();
  expect(sent).toEqual({ raw_seconds: 60 * 86_400 });
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

it.each([{}, { ...defaultRetention, raw: { seconds: "forever" } }, { ...defaultRetention, tables: [{ name: "users", rows: 1, bytes: 1, rows_per_day: 0 }] }])(
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
