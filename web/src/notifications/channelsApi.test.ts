import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createChannel,
  deleteChannel,
  fetchChannels,
  setQuietHours,
  testChannel,
  updateChannel,
} from "./channelsApi";

function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
    ...init,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("fetchChannels", () => {
  it("refuses a 200 with no channel list rather than reading it as empty", async () => {
    // "Alerts are going nowhere" on an instance with four working channels is
    // the most alarming wrong thing this page can say.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ ok: true }));
    await expect(fetchChannels()).rejects.toThrow(/no channel list/);
  });

  it("reports a failed list rather than rendering an empty instance", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ error: "nope" }, { status: 500 }),
    );
    await expect(fetchChannels()).rejects.toThrow("HTTP 500");
  });

  it("reads the channels the server sent", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ channels: [{ id: 3, name: "ops", type: "email" }] }),
    );
    const channels = await fetchChannels();
    expect(channels.map((c) => c.name)).toEqual(["ops"]);
  });
});

describe("setQuietHours", () => {
  it("puts the window as JSON on the channel's quiet-hours resource", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({ start: "23:00" }));
    const night = {
      start: "23:00",
      end: "07:00",
      timezone: "Europe/Amsterdam",
      during: "hold",
    };
    await setQuietHours("4", night);
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/v1/channels/4/quiet-hours");
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toEqual(night);
  });

  it("deletes the window when given null", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(null, { status: 204 }));
    await setQuietHours("4", null);
    const [url, init] = fetch.mock.calls[0];
    expect(String(url)).toBe("/api/v1/channels/4/quiet-hours");
    expect(init?.method).toBe("DELETE");
    expect(init?.body).toBeUndefined();
  });

  it("surfaces the server's refusal", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ error: "unknown IANA timezone" }, { status: 400 }),
    );
    await expect(
      setQuietHours("4", {
        start: "23:00",
        end: "07:00",
        timezone: "Mars/Olympus",
        during: "hold",
      }),
    ).rejects.toThrow(/unknown IANA timezone/);
  });
});

describe("testChannel", () => {
  it("reports the upstream error verbatim on a refused delivery", async () => {
    // A test that only says "failed" sends you to the server log, which is the
    // exact trip this button exists to save.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json(
        { ok: false, error: "401 unauthorized: bot token revoked" },
        { status: 502 },
      ),
    );
    const result = await testChannel("1");
    expect(result).toEqual({
      ok: false,
      error: "401 unauthorized: bot token revoked",
    });
  });

  it("does not throw on a 502, because a refused delivery is a result", async () => {
    // Going through apiRequest would turn it into an exception, and the caller
    // would have to reconstruct "the far end said no" from a transport error.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ ok: false, error: "no route to host" }, { status: 502 }),
    );
    await expect(testChannel("1")).resolves.toMatchObject({ ok: false });
  });

  it("refuses to read an unclear reply as success", async () => {
    // A 200 whose body does not say ok proves nothing. Reading it as a pass
    // would put a green tick on a channel nobody verified.
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({}));
    const result = await testChannel("1");
    expect(result.ok).toBe(false);
  });

  it("does not claim success when the body is not JSON at all", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("<html>502</html>", { status: 502 }),
    );
    const result = await testChannel("1");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error).toMatch(/HTTP 502/);
  });

  it("reports a pass only when the server says ok", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(json({ ok: true }));
    await expect(testChannel("1")).resolves.toEqual({ ok: true });
  });

  it("posts to the channel's own test endpoint", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({ ok: true }));
    await testChannel("9");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/channels/9/test");
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST");
  });
});

describe("createChannel and updateChannel", () => {
  it("sends the whole object to PUT, which is what the server expects", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({ id: 1, name: "a", type: "slack" }));
    await updateChannel("1", {
      name: "a",
      type: "slack",
      config: { url: "****B07F" },
    });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/channels/1");
    expect(init.method).toBe("PUT");
    // The mask travels back untouched: handleUpdateChannel restores the stored
    // credential when it sees a value that still equals its own mask, and any
    // other spelling either wipes the secret or fails validation.
    expect(JSON.parse(String(init.body))).toEqual({
      name: "a",
      type: "slack",
      config: { url: "****B07F" },
    });
  });

  it("posts a new channel to the collection", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(json({ id: 2, name: "b", type: "email" }, { status: 201 }));
    await createChannel({ name: "b", type: "email", config: { to: "a@b.c" } });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/channels");
    expect(init.method).toBe("POST");
    // The config travels with it. A create that sent only name and type would
    // be rejected by validateChannel — or, worse on a server that stopped
    // validating, would store a channel with no destination at all.
    expect(JSON.parse(String(init.body))).toEqual({
      name: "b",
      type: "email",
      config: { to: "a@b.c" },
    });
  });

  it("surfaces the server's own rejection sentence", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      json({ error: "config.url must use http or https" }, { status: 400 }),
    );
    await expect(
      createChannel({ name: "b", type: "slack", config: { url: "ftp://x" } }),
    ).rejects.toThrow("config.url must use http or https");
  });
});

describe("deleteChannel", () => {
  it("deletes and tolerates the empty 204 body", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(null, { status: 204 }));
    await deleteChannel("4");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/channels/4");
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("DELETE");
  });
});
