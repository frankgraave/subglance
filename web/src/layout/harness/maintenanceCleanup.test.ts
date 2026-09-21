import { expect, it, vi } from "vitest";
import { maintenanceCleanup } from "./maintenanceCleanup";

it("attempts every deletion and retries failed IDs even if the next list fails", async () => {
  const request = vi.fn<typeof fetch>()
    .mockResolvedValueOnce(Response.json({ maintenance: [{ id: 1 }, { id: 2 }] }))
    .mockRejectedValueOnce(new Error("connection lost"))
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValueOnce(new Response(null, { status: 503 }))
    .mockResolvedValueOnce(new Response(null, { status: 404 }))
    .mockResolvedValueOnce(Response.json({ maintenance: [] }));
  const cleanup = maintenanceCleanup("http://fixture", "session", request);
  expect(await cleanup()).toEqual(["delete maintenance 1: Error: connection lost"]);
  expect(request.mock.calls[2][0]).toBe("http://fixture/api/v1/maintenance/2");
  expect(await cleanup()).toEqual(["list maintenance: Error: HTTP 503"]);
  expect(request.mock.calls[4][0]).toBe("http://fixture/api/v1/maintenance/1");
  expect(await cleanup()).toEqual([]);
  expect(request).toHaveBeenCalledTimes(6);
});

it("keeps unresolved deletion failures visible for final teardown", async () => {
  const request = vi.fn<typeof fetch>().mockImplementation(async (_url, options) =>
    options?.method === "DELETE" ? new Response(null, { status: 500 }) : Response.json({ maintenance: [{ id: 7 }] }));
  const cleanup = maintenanceCleanup("http://fixture", "session", request);
  expect(await cleanup()).toEqual(["delete maintenance 7: Error: HTTP 500"]);
  expect(await cleanup()).toEqual(["delete maintenance 7: Error: HTTP 500"]);
});
