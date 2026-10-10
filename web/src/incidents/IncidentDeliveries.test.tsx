// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IncidentStoryItem } from "./IncidentStoryItem";
import { IncidentDeliveriesScope } from "./deliveriesScope";
import { deliveryFromApi } from "./deliveries";
import type { Incident } from "../monitors/detail";

/**
 * Where an incident's alerts went (SUB-217). What must hold: the list is in
 * the expanded row and only there, it is fetched for the row that opened,
 * every outcome is a word as well as a colour, and nothing that did not
 * arrive is drawn as if it had.
 */

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const T0 = Date.UTC(2026, 8, 16, 12, 0, 0);

const incident: Incident = {
  id: "7",
  monitorId: "1",
  startedAt: T0 - 3_600_000,
  confirmedAt: T0 - 3_540_000,
  resolvedAt: null,
  ackedAt: null,
  confirmed: true,
  resolved: false,
  acked: false,
  durationS: 3600,
  cause: "dns",
  lastError: "lookup api.example.com: no such host",
};

const DELIVERIES = {
  incident_id: 7,
  window_days: 30,
  complete: true,
  deliveries: [
    {
      id: 1, channel_id: 1, channel_name: "On-call", channel_type: "slack",
      event: "incident_confirmed", state: "delivered", attempts: 1,
      queued_at: new Date(T0 - 3_540_000).toISOString(),
      ended_at: new Date(T0 - 3_539_000).toISOString(), error: "", reason: "",
    },
    {
      id: 2, channel_id: 2, channel_name: "Pager", channel_type: "webhook",
      event: "incident_confirmed", state: "failed", attempts: 5,
      queued_at: new Date(T0 - 3_540_000).toISOString(),
      ended_at: new Date(T0 - 3_000_000).toISOString(),
      error: "gave up after 5 attempts: 503 Service Unavailable", reason: "",
    },
    {
      id: 3, channel_id: 3, channel_name: "", channel_type: "",
      event: "incident_reminder", state: "not_sent", attempts: 0,
      queued_at: new Date(T0 - 600_000).toISOString(),
      ended_at: new Date(T0 - 600_000).toISOString(), error: "",
      reason: "dropped during quiet hours",
    },
  ],
};

function stubFetch(body: unknown, status = 200) {
  const fetch = vi.fn(async (_input: RequestInfo | URL) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function renderRow(inScope: boolean, row: Incident = incident) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <IncidentDeliveriesScope.Provider value={inScope}>
        <ul>
          <IncidentStoryItem incident={row} now={T0} subject="api" />
        </ul>
      </IncidentDeliveriesScope.Provider>
    </QueryClientProvider>,
  );
}

function open() {
  fireEvent.click(screen.getByRole("button", { expanded: false }));
}

describe("an incident's notifications", () => {
  it("are fetched for the row only once it opens", async () => {
    const fetch = stubFetch(DELIVERIES);
    renderRow(true);
    expect(fetch).not.toHaveBeenCalled();
    open();
    const list = await screen.findByRole("list", { name: "Notifications" });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0]?.[0])).toBe("/api/v1/incidents/7/deliveries");

    const lines = within(list).getAllByRole("listitem").map((li) => li.textContent ?? "");
    expect(lines).toHaveLength(3);
    expect(lines[0]).toContain("Alert to On-call (Slack)");
    expect(lines[0]).toContain("Delivered");
    expect(lines[1]).toContain("Alert to Pager (Webhook)");
    expect(lines[1]).toContain("Failed");
    expect(lines[1]).toContain("503 Service Unavailable");
    expect(lines[2]).toContain("Reminder to a deleted channel");
    expect(lines[2]).toContain("Not sent");
    expect(lines[2]).toContain("Dropped during quiet hours");
  });

  it("are not offered where no screen owns the data", () => {
    const fetch = stubFetch(DELIVERIES);
    renderRow(false);
    open();
    expect(screen.queryByText("Notifications")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("say so when nothing was sent, and why on an unconfirmed failure", async () => {
    stubFetch({ ...DELIVERIES, deliveries: [] });
    renderRow(true);
    open();
    expect(await screen.findByText("No notification was sent about this incident.")).toBeTruthy();
    cleanup();

    stubFetch({ ...DELIVERIES, deliveries: [] });
    renderRow(true, { ...incident, confirmed: false, confirmedAt: null });
    open();
    expect(
      await screen.findByText("None: a failure is notified once it is confirmed."),
    ).toBeTruthy();
  });

  it("warn that the list may be short on an incident older than the outbox", async () => {
    stubFetch({ ...DELIVERIES, complete: false });
    renderRow(true);
    open();
    expect(
      await screen.findByText(/kept for 30 days, so earlier ones may be missing/),
    ).toBeTruthy();
  });

  it("report a failed read instead of an empty list", async () => {
    stubFetch({ error: "boom" }, 500);
    renderRow(true);
    open();
    expect(await screen.findByText(/could not be loaded: could not load notifications: HTTP 500/)).toBeTruthy();
    expect(screen.queryByText("No notification was sent about this incident.")).toBeNull();
  });
});

describe("deliveryFromApi", () => {
  const base = DELIVERIES.deliveries[0]!;

  it("never reads a state it does not know as delivered", () => {
    expect(deliveryFromApi({ ...base, state: "teleported" }).outcome).toBe("queued");
  });

  it("names what carried a merged delivery", () => {
    expect(
      deliveryFromApi({ ...base, state: "merged", merged_into: "recovery" }).detail,
    ).toMatch(/recovery message carried it/);
    expect(
      deliveryFromApi({ ...base, state: "merged", merged_into: "digest" }).detail,
    ).toMatch(/quiet-hours digest/);
  });

  it("dates a delivery still going out by when it was queued", () => {
    const d = deliveryFromApi({ ...base, state: "retrying", ended_at: null, error: "503" });
    expect(d.at).toBe(Date.parse(base.queued_at));
    expect(d.detail).toBe("503");
  });
});
