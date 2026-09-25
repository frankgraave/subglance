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
import { LiveNotificationsRoot } from "./LiveNotifications";
import { channelFromApi } from "./channels";
import { inventoryFromApi } from "../monitors/inventory";

/*
 * The data owner, tested through the screen it drives.
 *
 * Every assertion here is about a promise the page makes about the *server*
 * rather than about itself: that a test result is the server's own answer,
 * that a result belongs to the configuration it was measured against, and that
 * a viewer is offered nothing that would 403.
 */

afterEach(cleanup);

function client() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
}

function make(over: Record<string, unknown> = {}) {
  return channelFromApi({
    id: 1,
    name: "On-call Slack",
    type: "slack",
    config: { url: "****B07F" },
    enabled: true,
    ...over,
  });
}

describe("LiveNotifications", () => {
  it("shows the real failure of a test, in the server's words", async () => {
    const test = vi.fn().mockResolvedValue({
      ok: false,
      error: "401 unauthorized: bot token revoked",
    });
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={test}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /send test/i }));
    expect(
      await screen.findByText(/401 unauthorized: bot token revoked/),
    ).toBeTruthy();
    const row = screen.getByRole("listitem");
    expect(
      within(row).getByText("Test failed", { selector: ".chip" }),
    ).toBeTruthy();
  });

  it("never turns a failed request into a failed channel", async () => {
    // The request itself died — a dropped network, an expired session. That is
    // not evidence the channel is broken, and marking it failed is how someone
    // ends up re-pasting a working webhook.
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={async () => {
          throw new Error("Failed to fetch");
        }}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /send test/i }));
    expect(await screen.findByText("Failed to fetch")).toBeTruthy();
    const row = screen.getByRole("listitem");
    /*
     * No delivery chip at all, which is the shape this assertion took when the
     * always-"Not verified" column was removed (SUB-138). The row draws a chip
     * only for a result a test actually produced, so "nothing was proven"
     * renders as the absence of one rather than as a badge that said the same
     * thing on every row of every instance. A red "Test failed" here would be
     * the bug; so would a green one.
     */
    expect(
      within(row).queryByText("Test failed", { selector: ".chip" }),
    ).toBeNull();
    expect(
      within(row).queryByText("Test delivered", { selector: ".chip" }),
    ).toBeNull();
    // And the page still says, once, why nothing here is known to work.
    expect(screen.getByText(/carries no delivery history/i)).toBeTruthy();
  });

  it("drops a test result when the channel is edited", async () => {
    // The result was about the configuration stored a moment ago. Keeping it
    // would put a stale green tick beside a credential nothing has exercised.
    const update = vi.fn().mockResolvedValue(make());
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={async () => ({ ok: true }) as const}
        update={update}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /send test/i }));
    expect(await screen.findByText("Test delivered")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Edit/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /save changes/i }),
    );
    await waitFor(() => expect(update).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByText("Test delivered")).toBeNull(),
    );
  });

  it("offers a viewer nothing that would 403", async () => {
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        canWrite={false}
      />,
    );
    await screen.findByRole("listitem");
    expect(screen.queryByRole("button", { name: /send test/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Delete/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /add channel/i })).toBeNull();
  });

  it("reports a failed list rather than claiming the instance has no channels", async () => {
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => {
          throw new Error("could not load channels: HTTP 500");
        }}
      />,
    );
    expect(await screen.findByText(/HTTP 500/)).toBeTruthy();
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
  });

  it("sends the edit through PUT with the mask untouched", async () => {
    const update = vi.fn().mockResolvedValue(make());
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        update={update}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /^Edit/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /save changes/i }),
    );
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0][1].config).toEqual({ url: "****B07F" });
  });

  it("creates a channel through the add drawer", async () => {
    const create = vi.fn().mockResolvedValue(make({ id: 2 }));
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => []}
        create={create}
        createOpen
      />,
    );
    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Ops email" },
    });
    fireEvent.change(screen.getByLabelText(/recipient address/i), {
      target: { value: "ops@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^Add channel$/ }));
    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toEqual({
      name: "Ops email",
      type: "email",
      config: { to: "ops@example.com" },
    });
  });

  it("deletes a channel and forgets its test result with it", async () => {
    // Ids are reused by the database, and a stale "Test delivered" surviving
    // under a reused id would decorate a brand-new channel with somebody
    // else's proof.
    const remove = vi.fn().mockResolvedValue(undefined);
    let channels = [make()];
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => channels}
        test={async () => ({ ok: true }) as const}
        remove={remove}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: /send test/i }));
    expect(await screen.findByText("Test delivered")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Delete Slack/ }));
    // The confirmation requires the name (DESIGN.md §7.5).
    fireEvent.change(await screen.findByLabelText(/to confirm/i), {
      target: { value: "On-call Slack" },
    });
    fireEvent.click(
      await screen.findByRole("button", { name: /Delete On-call Slack/ }),
    );
    await waitFor(() => expect(remove).toHaveBeenCalledWith("1"));

    channels = [make({ id: 1, name: "Someone else", config: {} })];
    await waitFor(() =>
      expect(screen.queryByText("Test delivered")).toBeNull(),
    );
  });
});

describe("enabling and disabling through the server", () => {
  /*
   * The write that had no path at all before this: the row could report
   * "Disabled" and offer no way back.
   *
   * The assertion that matters is not that a PUT happened but *what was in
   * it*. The channel is sent back as it was read, masked config included,
   * because `handleUpdateChannel` treats a value still equal to its own mask
   * as unchanged and substitutes the stored secret. A request that dropped
   * the config, or sent an empty one, would destroy the webhook token on
   * every toggle — which is exactly the kind of damage that looks like
   * nothing until somebody needs an alert.
   */
  it("sends the channel back unchanged apart from the flag", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={async () => ({ ok: true }) as const}
        update={update}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: /^Disable / }));

    await waitFor(() => expect(update).toHaveBeenCalled());
    const [id, input] = update.mock.calls[0];
    expect(id).toBe("1");
    expect(input).toMatchObject({
      name: "On-call Slack",
      type: "slack",
      enabled: false,
    });
    // The masked secret goes back verbatim; the server restores the real one.
    expect(input.config).toEqual({ url: "****B07F" });
  });

  it("turns a disabled channel back on", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make({ enabled: false })]}
        test={async () => ({ ok: true }) as const}
        update={update}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: /^Enable / }));
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0][1]).toMatchObject({ enabled: true });
  });

  it("reports a refused toggle on the row rather than silently reverting", async () => {
    /*
     * A failed write that leaves the switch where it was, with no message,
     * teaches the reader the button is broken — or worse, that the channel is
     * off when it is not.
     */
    const update = vi.fn().mockRejectedValue(new Error("channel is locked"));
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={async () => ({ ok: true }) as const}
        update={update}
      />,
    );

    fireEvent.click(await screen.findByRole("button", { name: /^Disable / }));
    expect(await screen.findByText(/channel is locked/i)).toBeTruthy();
  });
});

describe("the default channel", () => {
  /*
   * The default is where a monitor with no channels of its own sends its
   * alerts (SUB-124). What matters is which request each choice sends: moving
   * it names the new channel, and clearing it names the channel that holds it
   * now — never "whatever the default is".
   */
  it("moves the default to the chosen channel", async () => {
    const setDefault = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make(), make({ id: 2, name: "Pager" })]}
        setDefault={setDefault}
      />,
    );
    const select = await screen.findByRole("combobox", {
      name: /monitors with no channels of their own/i,
    });
    fireEvent.change(select, { target: { value: "2" } });
    await waitFor(() => expect(setDefault).toHaveBeenCalledWith("2", true));
  });

  it("refreshes the monitor inventory, which names the default", async () => {
    const qc = client();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const setDefault = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={qc}
        list={async () => [make(), make({ id: 2, name: "Pager" })]}
        setDefault={setDefault}
      />,
    );
    const select = await screen.findByRole("combobox", {
      name: /monitors with no channels of their own/i,
    });
    fireEvent.change(select, { target: { value: "2" } });
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({
        queryKey: ["monitors", "inventory"],
      }),
    );
  });

  it("refreshes the monitor inventory when a channel is deleted", async () => {
    // The deleted channel may have been the default the inventory names.
    const qc = client();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const remove = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={qc}
        list={async () => [make()]}
        remove={remove}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /^Delete Slack/ }),
    );
    fireEvent.change(await screen.findByLabelText(/to confirm/i), {
      target: { value: "On-call Slack" },
    });
    fireEvent.click(
      await screen.findByRole("button", { name: /Delete On-call Slack/ }),
    );
    await waitFor(() => expect(remove).toHaveBeenCalledWith("1"));
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({
        queryKey: ["monitors", "inventory"],
      }),
    );
  });

  it("clears the default by naming the channel that holds it", async () => {
    const setDefault = vi.fn().mockResolvedValue(undefined);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [
          make({ is_default: true }),
          make({ id: 2, name: "Pager" }),
        ]}
        setDefault={setDefault}
      />,
    );
    const select = await screen.findByRole("combobox", {
      name: /monitors with no channels of their own/i,
    });
    await waitFor(() => expect((select as HTMLSelectElement).value).toBe("1"));
    fireEvent.change(select, { target: { value: "" } });
    await waitFor(() => expect(setDefault).toHaveBeenCalledWith("1", false));
  });

  it("reports a refused change instead of silently reverting", async () => {
    const setDefault = vi
      .fn()
      .mockRejectedValue(new Error("channel not found"));
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        setDefault={setDefault}
      />,
    );
    fireEvent.change(
      await screen.findByRole("combobox", {
        name: /monitors with no channels of their own/i,
      }),
      { target: { value: "1" } },
    );
    expect(await screen.findByText(/channel not found/i)).toBeTruthy();
  });

  it("shows a viewer the default as a sentence, with nothing to change", async () => {
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make({ is_default: true })]}
        canWrite={false}
      />,
    );
    expect(
      await screen.findByText(
        /monitors with no channels of their own alert through/i,
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("combobox")).toBeNull();
  });
});

describe("who hears what", () => {
  it("reads the monitor list and names the monitor nobody hears about", async () => {
    const monitors = vi.fn().mockResolvedValue([
      inventoryFromApi({
        id: 5,
        name: "billing",
        type: "http",
        target: "https://billing.example.com",
        interval_s: 60,
        timeout_s: 10,
        enabled: true,
        status: "up",
        created_at: "2026-09-01T10:00:00Z",
        channels: [],
      }),
    ]);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        monitors={monitors}
      />,
    );
    expect(
      await screen.findByText("1 active monitor alerts nobody."),
    ).toBeTruthy();
    expect(
      screen.getByText("nobody: no channels of its own and no default"),
    ).toBeTruthy();
    expect(monitors).toHaveBeenCalled();
    // DESIGN.md §8.3: a card framing a list is titled `Name (N)`.
    expect(
      screen.getByRole("heading", { name: "Who hears what (1)" }),
    ).toBeTruthy();
  });

  it("marks a paused monitor's row so it can carry the paused edge", async () => {
    const monitors = vi.fn().mockResolvedValue([
      inventoryFromApi({
        id: 6,
        name: "legacy",
        type: "http",
        target: "https://legacy.example.com",
        interval_s: 60,
        timeout_s: 10,
        enabled: false,
        status: "up",
        created_at: "2026-09-01T10:00:00Z",
        channels: [],
      }),
    ]);
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        monitors={monitors}
      />,
    );
    const name = await screen.findByText("legacy");
    expect(name.closest("li")?.getAttribute("data-paused")).toBe("true");
  });
});
