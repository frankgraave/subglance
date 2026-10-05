// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChannelFailingNotice } from "./ChannelFailingNotice";
import { channelsQueryKey } from "./channelsApi";
import { describeHistory, historyFromApi } from "./channels";

/*
 * The dashboard's line for a channel that stopped delivering (SUB-212). What
 * matters: it says nothing unless the server vouches for a failure, it names
 * the channel and whether anyone was told, and it says so loudest when there
 * was nobody to tell.
 */

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const delivery = (over: Record<string, unknown> = {}) => ({
  state: "failed",
  window_days: 30,
  last_delivered_at: null,
  last_failed_at: "2026-10-05T03:00:00Z",
  failed: 1,
  pending: 0,
  retrying: 0,
  last_error: "endpoint rejected the alert (404)",
  failing_since: null,
  notice_sent_at: null,
  notice_channel_id: null,
  notice: null,
  ...over,
});

const channel = (id: number, name: string, over: Record<string, unknown> = {}, enabled = true) => ({
  id,
  name,
  type: "slack",
  config: { url: "****0f3a" },
  enabled,
  delivery: delivery(over),
});

function show(channels: unknown, onOpen?: () => void) {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify(channels)));
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <ChannelFailingNotice onOpen={onOpen} />
    </QueryClientProvider>,
  );
  return { view, client, fetchMock };
}

/** Waits until the list has been fetched and the query has settled. */
async function settled({ client, fetchMock }: ReturnType<typeof show>) {
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  await waitFor(() => expect(client.isFetching()).toBe(0));
}

describe("ChannelFailingNotice", () => {
  it("names a failing channel and the channel that carried the notice", async () => {
    const onOpen = vi.fn();
    show(
      {
        channels: [
          channel(1, "ops-slack", {
            failing_since: "2026-10-05T03:00:00Z",
            notice: "sent",
            notice_sent_at: "2026-10-05T03:01:00Z",
            notice_channel_id: 2,
          }),
          channel(2, "Ops mailing list", { state: "delivered", failed: 0 }),
        ],
      },
      onOpen,
    );
    const line = await screen.findByRole("status");
    expect(line.textContent).toContain("Alerts to ops-slack are not arriving");
    expect(line.textContent).toContain("Reported through Ops mailing list");

    const link = screen.getByRole("link", { name: "Open notifications" });
    expect(link.getAttribute("href")).toBe("/notifications");
    fireEvent.click(link);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("says when there is no other channel to report it through", async () => {
    show({
      channels: [channel(1, "only-channel", { failing_since: "2026-10-05T03:00:00Z", notice: "no_other_channel" })],
    });
    expect((await screen.findByRole("status")).textContent).toContain(
      "No other channel to report this through",
    );
  });

  it("counts the channels when more than one is failing", async () => {
    show({
      channels: [
        channel(1, "b-hook", { failing_since: "2026-10-05T04:00:00Z", notice: "no_other_channel" }),
        channel(2, "a-hook", { failing_since: "2026-10-05T03:00:00Z", notice: "no_other_channel" }),
      ],
    });
    const text = (await screen.findByRole("status")).textContent ?? "";
    // Oldest failure first, and the unreported ones counted.
    expect(text).toContain("Alerts to 2 channels are not arriving: a-hook, b-hook.");
    expect(text).toContain("2 of them cannot be reported through another channel");
  });

  it("draws nothing for a 30-day failure that has since ended, a disabled channel, or a list it could not read", async () => {
    // Failed inside the window but delivered since: failing_since is null.
    const quiet = show({
      channels: [
        channel(1, "recovered", { failing_since: null }),
        channel(2, "switched-off", { failing_since: "2026-10-05T03:00:00Z", notice: "waiting" }, false),
      ],
    });
    await settled(quiet);
    expect(screen.queryByRole("status")).toBeNull();
    quiet.view.unmount();

    await settled(show({ error: "no list" }));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("takes the line down when a later fetch of the list fails, rather than repeating the last answer", async () => {
    const shown = show({
      channels: [channel(1, "ops-slack", { failing_since: "2026-10-05T03:00:00Z", notice: "no_other_channel" })],
    });
    await screen.findByRole("status");

    shown.fetchMock.mockImplementation(async () => new Response("", { status: 503 }));
    await shown.client.refetchQueries({ queryKey: channelsQueryKey });
    expect(shown.client.getQueryState(channelsQueryKey)?.status).toBe("error");
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });
});

describe("a failing channel's history", () => {
  it("reads the spell and its notice, and an older server's record as not failing", () => {
    const failing = historyFromApi(
      delivery({ failing_since: "2026-10-05T03:00:00Z", notice: "waiting" }),
    );
    expect(failing.failingSince).toBe(Date.parse("2026-10-05T03:00:00Z"));
    expect(failing.notice).toBe("waiting");
    expect(describeHistory(failing)).toMatch(/gave up .*Not reported yet/);

    const older = historyFromApi({ state: "failed", failed: 1 });
    expect(older.failingSince).toBeNull();
    expect(older.notice).toBeNull();
    expect(describeHistory(older)).not.toMatch(/reported/i);
  });
});
