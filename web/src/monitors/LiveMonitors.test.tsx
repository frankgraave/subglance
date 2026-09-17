// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render as renderBare,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { act } from "react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";
import { inventoryFromApi } from "./inventory";
import type { VersionedMonitor } from "./inventoryApi";

import { ShellSlots } from "../shell/ShellSlots";

/*
 * Every render gets the shell's two portal targets (SUB-138).
 *
 * This screen contributes its search to the masthead and its filters to the
 * page toolbar. Without the slots `TopbarTools` renders null — which is
 * correct for the status wall and wrong here — so the controls would vanish
 * and every assertion about them would pass by not looking.
 */
function render(ui: React.ReactElement) {
  return renderBare(
    <>
      <ShellSlots />
      {ui}
    </>,
  );
}


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

  it("fills the edit form from the same read the ETag came from", async () => {
    /*
     * The list row and the validator must describe one moment. Filling the
     * form from a row read a minute ago while sending an ETag read just now
     * would let a conditional PATCH sail straight through and overwrite a
     * change the user never saw — a precondition that guards the wrong instant
     * is worse than none, because it looks like it worked.
     */
    const patch = vi.fn(
      (_id: string, _body: unknown, _version?: string | null) =>
        Promise.resolve(),
    );
    const forEdit = vi.fn((_id: string) =>
      Promise.resolve({
        // Renamed elsewhere since the list was fetched. The form must show
        // this name, not the stale one.
        monitor: make({ name: "auth-renamed-elsewhere" }),
        etag: 'W/"1757606400"',
      }),
    );

    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() => Promise.resolve([make()])}
        fetchChannels={noChannels}
        patch={patch}
        forEdit={forEdit}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Edit auth" }));
    const dialog = await screen.findByRole("dialog");
    const nameInput = within(dialog).getByLabelText("Name") as HTMLInputElement;
    expect(nameInput.value).toBe("auth-renamed-elsewhere");

    fireEvent.change(nameInput, { target: { value: "auth-eu" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: /save changes/i }),
    );

    await waitFor(() => expect(patch).toHaveBeenCalled());
    expect(patch.mock.calls[0][2]).toBe('W/"1757606400"');
    expect(forEdit).toHaveBeenCalledWith("1");
  });

  it("lets a slow edit load overwrite neither the drawer nor the error of a newer one", async () => {
    /*
     * Press Edit on A, then on B before A's read lands. A's monitor arriving
     * second would put A's values under B's title and save them with a third
     * monitor's ETag — the exact confusion the conditional PATCH exists to
     * prevent, arriving through the front door.
     */
    const resolvers: Record<string, (v: VersionedMonitor) => void> = {};
    const forEdit = vi.fn(
      (id: string) =>
        new Promise<VersionedMonitor>((resolve) => {
          resolvers[id] = resolve;
        }),
    );

    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() =>
          Promise.resolve([make(), make({ id: 2, name: "cdn" })])
        }
        fetchChannels={noChannels}
        forEdit={forEdit}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Edit auth" }));
    fireEvent.click(screen.getByRole("button", { name: "Edit cdn" }));

    // B lands first, then A — the out-of-order case.
    await act(async () => {
      resolvers["2"]({ monitor: make({ id: 2, name: "cdn" }), etag: 'W/"2"' });
    });
    await act(async () => {
      resolvers["1"]({ monitor: make(), etag: 'W/"1"' });
    });

    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toMatch(/Edit cdn/);
    expect(dialog.textContent).not.toMatch(/Edit auth/);
  });

  it("does not close a second monitor's drawer when the first save lands", async () => {
    /*
     * Cancel stays live while a save is in flight, so the user can close A and
     * open B before A finishes. Closing B's drawer because A landed would
     * throw away edits B had already typed — and the user would have no idea
     * why the form vanished.
     */
    let finishSave: () => void = () => {};
    const patch = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finishSave = resolve;
        }),
    );

    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() =>
          Promise.resolve([make(), make({ id: 2, name: "cdn" })])
        }
        fetchChannels={noChannels}
        patch={patch}
        forEdit={(id) =>
          Promise.resolve({
            monitor: id === "1" ? make() : make({ id: 2, name: "cdn" }),
            etag: `W/"${id}"`,
          })
        }
      />,
    );

    // Start a save on A…
    fireEvent.click(await screen.findByRole("button", { name: "Edit auth" }));
    const first = await screen.findByRole("dialog");
    fireEvent.change(within(first).getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(
      within(first).getByRole("button", { name: /save changes/i }),
    );
    await waitFor(() => expect(patch).toHaveBeenCalled());

    // …then leave it and open B while A is still in flight.
    fireEvent.click(within(first).getByRole("button", { name: /cancel/i }));
    fireEvent.click(screen.getByRole("button", { name: "Edit cdn" }));
    await waitFor(() =>
      expect(screen.getByRole("dialog").textContent).toMatch(/Edit cdn/),
    );

    await act(async () => {
      finishSave();
    });

    // B is still open, with its own values.
    const open = screen.getByRole("dialog");
    expect(open.textContent).toMatch(/Edit cdn/);
  });

  it("says so when the monitor cannot be re-read for editing", async () => {
    // A button that opens no drawer reads as a broken page.
    render(
      <LiveMonitorsRoot
        client={client()}
        fetchMonitors={() => Promise.resolve([make()])}
        fetchChannels={noChannels}
        forEdit={() => Promise.reject(new Error("monitor not found"))}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Edit auth" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/monitor not found/);
    expect(screen.queryByRole("dialog")).toBeNull();
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
