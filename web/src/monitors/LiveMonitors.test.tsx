// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";
import { inventoryFromApi } from "./inventory";

/*
 * The data owner, tested through the screen it drives.
 *
 * Every assertion here is about a promise the page makes about the *server*
 * rather than about itself: that a pause is only shown once the server
 * confirmed it, that a conditional PATCH carries the version that was just
 * read, and that a failed write is attributed to the row it belongs to.
 */

afterEach(cleanup);

function client() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
}

function make(over: Record<string, unknown> = {}) {
  return inventoryFromApi({
    id: 1,
    name: "auth",
    type: "http",
    target: "https://auth.example.com",
    interval_s: 60,
    timeout_s: 10,
    enabled: true,
    status: "up",
    created_at: "2026-09-01T10:00:00Z",
    ...over,
  } as Parameters<typeof inventoryFromApi>[0]);
}

const noChannels = () =>
  Promise.resolve({ byMonitor: {}, truncated: false });

describe("LiveMonitors", () => {
  it("refetches after a pause instead of flipping the row locally", async () => {
    /*
     * The optimistic version is tempting and is the one place on this page
     * where guessing is unacceptable: "paused" is a claim that SubGlance has
     * stopped watching something, and a row saying so because the browser
     * assumed a request would succeed is the product lying about whether
     * anyone is watching.
     */
    let enabled = true;
    const fetchMonitors = vi.fn(() =>
      Promise.resolve([make({ enabled })]),
    );
    const pause = vi.fn(() => {
      enabled = false;
      return Promise.resolve();
    });

    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={fetchMonitors}
        fetchChannels={noChannels}
        pause={pause}
      />,
    );

    const button = await screen.findByRole("button", { name: "Pause auth" });
    fireEvent.click(button);
    await waitFor(() => expect(pause).toHaveBeenCalledWith("1", true));
    // It only says Resume once a refetch came back saying so.
    await screen.findByRole("button", { name: "Resume auth" });
    expect(fetchMonitors.mock.calls.length).toBeGreaterThan(1);
  });

  it("reads the ETag and sends it back with the edit", async () => {
    // Without a validator the endpoint is last-write-wins, and two people
    // tidying the same inventory is exactly the situation this page creates.
    const patch = vi.fn(
      (_id: string, _body: unknown, _version?: string | null) =>
        Promise.resolve(),
    );
    const version = vi.fn((_id: string) =>
      Promise.resolve<string | null>('W/"1757606400"'),
    );

    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() => Promise.resolve([make()])}
        fetchChannels={noChannels}
        patch={patch}
        version={version}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Edit auth" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: /save changes/i }),
    );

    await waitFor(() => expect(patch).toHaveBeenCalled());
    expect(patch.mock.calls[0][2]).toBe('W/"1757606400"');
    expect(version).toHaveBeenCalledWith("1");
  });

  it("puts a failed pause on the row it failed for", async () => {
    // One shared error string would attribute the failure to whichever row was
    // pressed last, and an error pointing at the wrong monitor is worse than
    // no error on a page whose job is to be trusted.
    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() =>
          Promise.resolve([make(), make({ id: 2, name: "cdn" })])
        }
        fetchChannels={noChannels}
        pause={() => Promise.reject(new Error("your role does not allow changes"))}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Pause cdn" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/does not allow changes/);
    const [first, second] = screen.getAllByRole("listitem");
    expect(within(second).queryByRole("alert")).not.toBeNull();
    expect(within(first).queryByRole("alert")).toBeNull();
  });

  it("does not refetch after a check that the server did not record", async () => {
    // A paused monitor's manual check changes nothing on the server, so a
    // refetch would only make the button feel slow.
    const fetchMonitors = vi.fn(() =>
      Promise.resolve([make({ enabled: false })]),
    );
    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={fetchMonitors}
        fetchChannels={noChannels}
        check={() =>
          Promise.resolve({ ok: true, latencyMs: 9, recorded: false })
        }
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Check now auth" }),
    );
    await screen.findByText(/Not recorded/);
    expect(fetchMonitors).toHaveBeenCalledTimes(1);
  });

  it("hides every write control from an account that may not write", async () => {
    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() => Promise.resolve([make()])}
        fetchChannels={noChannels}
        canWrite={false}
      />,
    );
    await screen.findByText("auth");
    expect(screen.queryByRole("button", { name: "Pause auth" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete auth" })).toBeNull();
    expect(screen.queryByRole("button", { name: /add monitor/i })).toBeNull();
  });

  it("surfaces a failed list rather than showing an empty instance", async () => {
    // "Nothing is being watched yet" on an instance with forty monitors is the
    // single most alarming wrong thing this screen could say.
    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() => Promise.reject(new Error("HTTP 500"))}
        fetchChannels={noChannels}
      />,
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/HTTP 500/);
    expect(screen.queryByText(/Nothing is being watched yet/)).toBeNull();
  });
});
