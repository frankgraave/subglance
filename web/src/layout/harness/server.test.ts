import { describe, expect, it } from "vitest";
import { serveBuild } from "./server";

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
