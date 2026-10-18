// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LiveDashboardRoot } from "../live/LiveDashboard";
import type { EventSourceLike } from "../live/connection";
import { disabledWatchdog } from "../watchdog/fixtures";
import { parseConnectivity } from "./api";
import { formatSince } from "./offlineState";
import {
  contradictedConnectivity, offlineConnectivity, onlineConnectivity, resolverDownConnectivity, unansweredConnectivity,
} from "./fixtures";

const clients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
});

const MONITOR = {
  id: 1,
  name: "api",
  type: "http",
  target: "https://api.example.com",
  interval_seconds: 60,
  status: "warning",
  latency_ms: null,
  uptime_24h: 99.9,
  created_at: "2026-09-01T00:00:00Z",
  heartbeats: [{ ts: "2026-09-29T01:13:00Z", ok: false, latency_ms: null }],
};

/** A stream the test drives: it opens or fails when told to. */
class Source implements EventSourceLike {
  static last: Source | null = null;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState = 0;
  constructor() {
    Source.last = this;
  }
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
  open(): void {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  fail(): void {
    this.readyState = 2;
    this.onerror?.(new Event("error"));
  }
}

function dashboard(
  connectivity: unknown,
  { status = 200, layout = "rows" } = {},
) {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  const answer = { body: connectivity, status };
  const fetcher = vi.fn(async (input: string) => {
    if (input === "/api/v1/connectivity") {
      return new Response(JSON.stringify(answer.body), { status: answer.status });
    }
    if (input === "/api/v1/watchdog") return new Response(JSON.stringify(disabledWatchdog));
    return new Response(JSON.stringify({ monitors: [MONITOR] }));
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <>
      <LiveDashboardRoot
        client={client}
        layout={layout as "rows" | "wall"}
        beatWidth={200}
        createEventSource={() => new Source()}
      />
    </>,
  );
  act(() => Source.last?.open());
  return { client, fetcher, answer };
}

const SENTENCE = /No outbound connection since/;

describe("the host-offline line", () => {
  it("says once, above the list, that the server cannot see out", async () => {
    dashboard(offlineConnectivity);
    const line = await screen.findByText(SENTENCE);
    expect(screen.getAllByText(SENTENCE)).toHaveLength(1);
    // Polite status, never an alert: a server that cannot see out is not an
    // outage of what it watches.
    const region = line.closest("[role]");
    expect(region?.getAttribute("role")).toBe("status");
    expect(region?.getAttribute("aria-live")).toBe("polite");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(region?.textContent).toContain("held as warnings, not outages");
    // Above the counts, like the connection badge: read before the numbers.
    const section = document.querySelector(".mon-dashboard")!;
    expect(section.firstElementChild?.nextElementSibling).toBe(region);
    // The monitors are left as they were: the line explains, not replaces.
    expect(await screen.findAllByText("api")).not.toHaveLength(0);
  });

  it.each([
    ["online", onlineConnectivity, 200],
    ["switched off", { enabled: false, offline: false, offline_since: null }, 200],
    ["unavailable", offlineConnectivity, 503],
    ["offline without a time", { enabled: true, offline: true, offline_since: null }, 200],
    ["offline on a disabled check", { ...offlineConnectivity, enabled: false }, 200],
    ["unparseable time", { ...offlineConnectivity, offline_since: "soon" }, 200],
    ["not an object", null, 200],
  ])("says nothing when the answer is %s", async (_name, body, status) => {
    const { client } = dashboard(body, { status });
    await waitFor(() => expect(client.getQueryState(["connectivity"])?.fetchStatus).toBe("idle"));
    await screen.findAllByText("api");
    expect(screen.queryByText(SENTENCE)).toBeNull();
  });

  it("withdraws the line when a later poll fails, rather than keep an answer nobody vouches for", async () => {
    const { client, answer } = dashboard(offlineConnectivity);
    await screen.findByText(SENTENCE);
    answer.status = 503;
    await act(() => client.refetchQueries({ queryKey: ["connectivity"] }));
    await waitFor(() => expect(screen.queryByText(SENTENCE)).toBeNull());
  });

  it("clears when the server reports it can see out again", async () => {
    const { client, answer } = dashboard(offlineConnectivity);
    await screen.findByText(SENTENCE);
    answer.body = onlineConnectivity;
    await act(() => client.refetchQueries({ queryKey: ["connectivity"] }));
    await waitFor(() => expect(screen.queryByText(SENTENCE)).toBeNull());
  });

  it("gives way to the connection badge while the stream is down", async () => {
    dashboard(offlineConnectivity);
    await screen.findByText(SENTENCE);
    act(() => Source.last!.fail());
    await screen.findByText(/Connection lost/);
    expect(screen.queryByText(SENTENCE)).toBeNull();
  });

  it("rides the status wall's meta line", async () => {
    dashboard(offlineConnectivity, { layout: "wall" });
    const meta = await screen.findByText(SENTENCE);
    expect(meta.closest(".wall-meta")).not.toBeNull();
  });
});

const RESOLVER = /SubGlance's DNS resolver has not answered since/;

describe("the resolver line", () => {
  it("names the resolver, not the connection, when only lookups fail", async () => {
    dashboard(resolverDownConnectivity);
    const line = await screen.findByText(RESOLVER);
    expect(screen.getAllByText(RESOLVER)).toHaveLength(1);
    expect(screen.queryByText(SENTENCE)).toBeNull();
    const region = line.closest("[role]");
    expect(region?.getAttribute("role")).toBe("status");
    expect(region?.textContent).toContain("failed lookups are held as warnings, not outages");
  });

  it.each([
    ["switched off", { ...resolverDownConnectivity, enabled: false }],
    ["down without a time", { ...resolverDownConnectivity, resolver_down_since: null }],
    ["claimed beside an offline host", { ...offlineConnectivity, resolver_down: true, resolver_down_since: "2026-09-29T01:12:04Z" }],
  ])("says nothing when the resolver answer is %s", async (_name, body) => {
    const { client } = dashboard(body);
    await waitFor(() => expect(client.getQueryState(["connectivity"])?.fetchStatus).toBe("idle"));
    await screen.findAllByText("api");
    expect(screen.queryByText(RESOLVER)).toBeNull();
    expect(screen.queryByText(SENTENCE)).toBeNull();
  });

  it("rides the status wall's meta line", async () => {
    dashboard(resolverDownConnectivity, { layout: "wall" });
    const meta = await screen.findByText(RESOLVER);
    expect(meta.closest(".wall-meta")).not.toBeNull();
  });
});

const UNANSWERED = /Connectivity targets have not answered since/;
const CONTRADICTED = /Connectivity targets look blocked since/;

describe("the unusable-targets line", () => {
  it.each([
    ["never answered", unansweredConnectivity, UNANSWERED, "failures are reported, not held back"],
    ["contradicted", contradictedConnectivity, CONTRADICTED, "other checks reach the internet, so failures are reported"],
  ])("says the targets %s, and that nothing is held back", async (_name, body, words, held) => {
    dashboard(body);
    const line = await screen.findByText(words);
    expect(screen.getAllByText(words)).toHaveLength(1);
    const region = line.closest("[role]");
    expect(region?.getAttribute("role")).toBe("status");
    expect(region?.textContent).toContain(held);
    expect(region?.textContent).not.toContain("held as warnings");
    expect(screen.queryByText(SENTENCE)).toBeNull();
  });

  it.each([
    ["switched off", { ...unansweredConnectivity, enabled: false }],
    ["claimed beside an offline host", { ...offlineConnectivity, targets_unusable: "unanswered", targets_unusable_since: "2026-10-18T03:34:43Z" }],
    ["without a time", { ...unansweredConnectivity, targets_unusable_since: null }],
    ["an unknown reason", { ...unansweredConnectivity, targets_unusable: "tired" }],
  ])("says nothing when the targets answer is %s", async (_name, body) => {
    const { client } = dashboard(body);
    await waitFor(() => expect(client.getQueryState(["connectivity"])?.fetchStatus).toBe("idle"));
    await screen.findAllByText("api");
    expect(screen.queryByText(UNANSWERED)).toBeNull();
    expect(screen.queryByText(SENTENCE)).toBeNull();
  });

  it("gives way to a silent resolver, which explains warnings it holds back", async () => {
    dashboard({ ...resolverDownConnectivity, targets_unusable: "contradicted", targets_unusable_since: "2026-10-18T03:34:43Z" });
    await screen.findByText(RESOLVER);
    expect(screen.queryByText(CONTRADICTED)).toBeNull();
  });
});

describe("parseConnectivity", () => {
  const answering = { resolverDown: false, resolverDownSince: null, targetsUnusable: null, targetsUnusableSince: null };

  it("accepts the three documented states", () => {
    expect(parseConnectivity(onlineConnectivity)).toEqual({
      enabled: true,
      offline: false,
      offlineSince: null,
      ...answering,
    });
    expect(parseConnectivity(offlineConnectivity)).toEqual({
      enabled: true,
      offline: true,
      offlineSince: Date.parse("2026-09-29T01:12:04Z"),
      ...answering,
    });
    expect(parseConnectivity(resolverDownConnectivity)).toEqual({
      enabled: true,
      offline: false,
      offlineSince: null,
      resolverDown: true,
      resolverDownSince: Date.parse("2026-09-29T01:12:04Z"),
      targetsUnusable: null,
      targetsUnusableSince: null,
    });
    expect(parseConnectivity(contradictedConnectivity)).toEqual({
      enabled: true,
      offline: false,
      offlineSince: null,
      resolverDown: false,
      resolverDownSince: null,
      targetsUnusable: "contradicted",
      targetsUnusableSince: Date.parse("2026-10-18T03:34:43Z"),
    });
  });

  it("refuses a targets answer it cannot vouch for", () => {
    expect(parseConnectivity({ ...onlineConnectivity, targets_unusable_since: "2026-10-18T03:34:43Z" })).toBeNull();
    expect(parseConnectivity({ ...unansweredConnectivity, targets_unusable_since: "soon" })).toBeNull();
    expect(parseConnectivity({ ...unansweredConnectivity, enabled: false })).toBeNull();
  });

  it("reads a server from before the resolver fields as one whose resolver answers", () => {
    expect(parseConnectivity({ enabled: true, offline: false, offline_since: null })).toEqual({
      enabled: true,
      offline: false,
      offlineSince: null,
      ...answering,
    });
  });

  it("refuses a resolver answer it cannot vouch for", () => {
    expect(parseConnectivity({ ...onlineConnectivity, resolver_down: "yes" })).toBeNull();
    expect(parseConnectivity({ ...onlineConnectivity, resolver_down_since: "2026-09-29T01:12:04Z" })).toBeNull();
    expect(parseConnectivity({ ...resolverDownConnectivity, resolver_down_since: "soon" })).toBeNull();
  });

  it("refuses an online answer that still carries a time", () => {
    expect(parseConnectivity({ ...onlineConnectivity, offline_since: "2026-09-29T01:12:04Z" })).toBeNull();
  });
});

describe("formatSince", () => {
  it("adds the day only when it is not today", () => {
    const now = new Date(2026, 8, 29, 9, 0).getTime();
    const today = formatSince(new Date(2026, 8, 29, 3, 12).getTime(), now);
    const yesterday = formatSince(new Date(2026, 8, 28, 23, 40).getTime(), now);
    expect(today).not.toMatch(/Sep/);
    expect(yesterday).toMatch(/28/);
    expect(yesterday).toMatch(/Sep/);
  });
});
