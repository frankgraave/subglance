import { describe, expect, it } from "vitest";
import { serveBuild } from "./server";

import { disabledWatchdog } from "../../watchdog/fixtures";

it("settles the watchdog query with explicit disabled history, never a hidden 404", async () => {
  const server = await serveBuild();
  try {
    const response = await fetch(`${server.url}/api/v1/watchdog`);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual(disabledWatchdog);
  } finally { await server.close(); }
});

describe("detail history harness fixture", () => {
  it("answers the response-history request without fabricating failures", async () => {
    const server = await serveBuild();
    try {
      const response = await fetch(`${server.url}/api/v1/monitors/1/heartbeats?limit=100`);
      expect(response.status).toBe(200);
      expect(await response.json()).toEqual({ heartbeats: [] });
      // Unknown API paths must still fail rather than quietly returning a fixture.
      expect((await fetch(`${server.url}/api/v1/monitors/1/unknown`)).status).toBe(404);
    } finally {
      await server.close();
    }
  });
});

it("serves resolved history for the incidents screen's selected window", async () => {
  const server = await serveBuild();
  try {
    for (const days of [1, 30, 90]) {
      const response = await fetch(
        `${server.url}/api/v1/incidents/resolved?days=${days}&limit=50`,
      );
      expect(response.status).toBe(200);
      const body = await response.json();
      expect(body.has_more).toBe(false);
      expect(body.next_cursor).toBeUndefined();
      expect(body.days).toBe(days);
      // The fixture recovered 25 hours ago; changing the window must matter.
      expect(body.incidents.map((incident: { id: number }) => incident.id))
        .toEqual(days === 1 ? [] : [41]);
      for (const incident of body.incidents) {
        expect(incident.resolved).toBe(true);
        expect(incident.resolved_at).toBeTypeOf("string");
      }
    }
  } finally {
    await server.close();
  }
});
