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
    const test = vi
      .fn()
      .mockResolvedValue({ ok: false, error: "401 unauthorized: bot token revoked" });
    render(
      <LiveNotificationsRoot
        client={client()}
        list={async () => [make()]}
        test={test}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /send test/i }),
    );
    expect(
      await screen.findByText(/401 unauthorized: bot token revoked/),
    ).toBeTruthy();
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Test failed", { selector: ".chip" })).toBeTruthy();
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
    expect(
      within(row).getByText("Not verified", { selector: ".chip" }),
    ).toBeTruthy();
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
    fireEvent.click(await screen.findByRole("button", { name: /save changes/i }));
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
    fireEvent.click(await screen.findByRole("button", { name: /save changes/i }));
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
    fireEvent.click(
      screen.getByRole("button", { name: /^Add channel$/ }),
    );
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
