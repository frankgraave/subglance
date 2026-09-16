// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { NotificationsView } from "./NotificationsView";
import { channelFromApi } from "./channels";
import type { Channel } from "./channels";

/*
 * The notifications screen, rendered from fixtures.
 *
 * Every assertion is about a claim the screen makes, not about markup: whether
 * an unverified channel can be mistaken for a healthy one, whether a stored
 * secret can be read back off the page, whether a failed test says what the
 * far end said, and whether a viewer is offered a button that would 403.
 */

function make(over: Record<string, unknown> = {}): Channel {
  return channelFromApi({
    id: 1,
    name: "On-call Slack",
    type: "slack",
    config: { url: "****B07F" },
    enabled: true,
    created_at: "2026-09-01T10:00:00Z",
    ...over,
  });
}

afterEach(cleanup);

describe("NotificationsView", () => {
  it("shows a channel's type and destination", () => {
    render(<NotificationsView channels={[make()]} />);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("On-call Slack")).toBeTruthy();
    expect(within(row).getByText("Slack")).toBeTruthy();
    expect(within(row).getByText("endpoint ending ****B07F")).toBeTruthy();
  });

  it("calls an untested channel 'Not verified', never healthy", () => {
    // The API carries no delivery history, so a green tick here would be
    // invented — and an invented green tick on a monitoring tool is how a dead
    // channel goes on looking fine for three days.
    render(<NotificationsView channels={[make()]} />);
    const row = screen.getByRole("listitem");
    const chip = within(row).getByText("Not verified", { selector: ".chip" });
    expect(within(row).queryByText(/delivered/i)).toBeNull();
    /*
     * The dashed "about the data" chip, specifically — not a status chip
     * wearing the same words.
     *
     * `StatusChip` carries `chip--status` and a `data-status` colour; a green
     * one labelled "Not verified" would pass a text assertion while telling
     * every sighted reader the channel is healthy, and one-meaning-per-signal
     * (DESIGN.md §2.3) is exactly what that breaks. The absence of a status is
     * the assertion.
     */
    expect(chip.className).toContain("chip--state");
    expect(chip.getAttribute("data-status")).toBeNull();
  });

  it("states once that delivery history is not available", () => {
    // Without the explanation, "Not verified" on every row reads as a bug in
    // this page rather than as a gap in the API.
    render(<NotificationsView channels={[make()]} />);
    expect(screen.getByText(/carries no delivery history/i)).toBeTruthy();
  });

  it("never renders a stored secret, only the mask the API returned", () => {
    render(
      <NotificationsView
        channels={[
          make({ config: { url: "****B07F" } }),
          make({
            id: 2,
            name: "bot",
            type: "telegram",
            config: { bot_token: "****9xQ2", chat_id: "-100123" },
          }),
        ]}
      />,
    );
    const text = document.body.textContent ?? "";
    expect(text).not.toContain("hooks.slack.com");
    // The chat id is public config and is deliberately shown in full: it names
    // a destination but grants nothing.
    expect(text).toContain("chat -100123");
  });

  it("says Disabled in words, not only by dimming the row", () => {
    render(<NotificationsView channels={[make({ enabled: false })]} />);
    const row = screen.getByRole("listitem");
    expect(
      within(row).getByText("Disabled", { selector: ".chip" }),
    ).toBeTruthy();
  });

  it("names the channel in every row action", () => {
    // Four rows of buttons all called "Test" is a list a screen reader user
    // cannot navigate and a voice-control user cannot address.
    render(
      <NotificationsView
        channels={[make(), make({ id: 2, name: "Ops email", type: "email" })]}
        onTest={() => {}}
        onDelete={() => {}}
        onSave={async () => {}}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Send test Slack On-call Slack" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Delete Email Ops email" }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Edit Slack On-call Slack" })).toBeTruthy();
  });

  it("prints the upstream error verbatim when a test fails", () => {
    render(
      <NotificationsView
        channels={[make()]}
        deliveries={{
          "1": { kind: "failed", error: "401 unauthorized: bot token revoked" },
        }}
      />,
    );
    expect(
      screen.getByText(/401 unauthorized: bot token revoked/),
    ).toBeTruthy();
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Test failed", { selector: ".chip" })).toBeTruthy();
  });

  it("does not let a passed test claim anything about earlier deliveries", () => {
    render(
      <NotificationsView channels={[make()]} deliveries={{ "1": { kind: "passed" } }} />,
    );
    expect(screen.getByText(/says nothing about deliveries made before/i)).toBeTruthy();
  });

  it("states the consequence when there are no channels at all", () => {
    // Not a polite grey murmur: no channel means every alert goes into a void
    // on an install that otherwise looks healthy.
    render(<NotificationsView channels={[]} onCreateOpenChange={() => {}} onSave={async () => {}} />);
    expect(screen.getByText("Alerts are going nowhere.")).toBeTruthy();
  });

  it("never shows the empty state for a failed load", () => {
    // "Alerts are going nowhere" to somebody with four working channels tells
    // them their alerting is gone when in fact one request 500'd.
    render(
      <NotificationsView channels={[]} error={new Error("HTTP 500")} />,
    );
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
    expect(screen.getByText("HTTP 500")).toBeTruthy();
  });

  it("never shows the empty state while still loading", () => {
    render(<NotificationsView channels={[]} loading />);
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
    expect(screen.getAllByText(/loading channels/i).length).toBeGreaterThan(0);
  });

  it("confirms a delete and names what goes silent with it", () => {
    const onDelete = vi.fn();
    render(
      <NotificationsView
        channels={[make()]}
        onDelete={onDelete}
        onSave={async () => {}}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Delete Slack On-call Slack" }),
    );
    // Nothing is deleted by the first press: the drawer explains that monitors
    // pointing only at this channel will alert nobody.
    expect(onDelete).not.toHaveBeenCalled();
    expect(screen.getByText(/will go on being checked and will tell nobody/i)).toBeTruthy();

    /*
     * The name has to be retyped (DESIGN.md §7.5). This deletion destroys
     * stored credentials and detaches every monitor pointing at the channel,
     * which is precisely the kind of consequence a reflex click skips past.
     */
    const confirm = screen.getByRole("button", {
      name: /Delete On-call Slack/,
    });
    expect(confirm.hasAttribute("disabled")).toBe(true);
    fireEvent.click(confirm);
    expect(onDelete).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText(/to confirm/i), {
      target: { value: "On-call Slack" },
    });
    expect(confirm.hasAttribute("disabled")).toBe(false);
    fireEvent.click(confirm);
    expect(onDelete).toHaveBeenCalledWith("1");
  });

  it("offers a viewer no write controls and no test button", () => {
    // A test sends a real message to somebody else's inbox, and the server
    // guards it with the write role for exactly that reason.
    render(<NotificationsView channels={[make()]} />);
    expect(screen.queryByRole("button", { name: /send test/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Edit/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Delete/ })).toBeNull();
    expect(screen.getByText(/may not send a test/i)).toBeTruthy();
  });

  it("builds no routing, quiet hours or severity controls, and says why", () => {
    // SUB-124 has no backend. A quiet-hours switch that silently changes
    // nothing is worse than no switch at all.
    render(<NotificationsView channels={[make()]} onSave={async () => {}} />);
    expect(screen.queryByRole("button", { name: /quiet hours/i })).toBeNull();
    expect(screen.queryByLabelText(/severity/i)).toBeNull();
    expect(screen.getByText(/no backend today \(SUB-124\)/i)).toBeTruthy();
  });

  it("puts the rows directly on the card — two surfaces, never three", () => {
    // Commit 1eaa151: a wrapper around a list of rows that already carry
    // border, radius and background is a forbidden third layer.
    const { container } = render(<NotificationsView channels={[make()]} />);
    const list = container.querySelector(".inv-list")!;
    expect(list.parentElement?.classList.contains("card")).toBe(true);
  });
});

describe("enabling and disabling a channel", () => {
  /*
   * A disabled channel was visible but unchangeable: the row said "Disabled"
   * and offered no way back, so the only route to a working channel again was
   * to delete it and retype the webhook. Disabling is also the honest
   * alternative to deleting when a channel is temporarily noisy — it keeps
   * the credentials and the monitor attachments that deleting destroys.
   */
  it("offers the state it moves to, not the state it is in", () => {
    const onSetEnabled = vi.fn();
    render(
      <NotificationsView
        channels={[make()]}
        onSetEnabled={onSetEnabled}
        onSave={async () => {}}
      />,
    );
    // Named after the channel: several identically-named buttons are a list a
    // screen reader cannot navigate and voice control cannot address.
    fireEvent.click(
      screen.getByRole("button", { name: "Disable Slack On-call Slack" }),
    );
    expect(onSetEnabled).toHaveBeenCalledWith("1", false);
  });

  it("offers a disabled channel a way back", () => {
    const onSetEnabled = vi.fn();
    render(
      <NotificationsView
        channels={[make({ enabled: false })]}
        onSetEnabled={onSetEnabled}
        onSave={async () => {}}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Enable Slack On-call Slack" }),
    );
    expect(onSetEnabled).toHaveBeenCalledWith("1", true);
  });

  it("says it is saving and refuses a second press", () => {
    const onSetEnabled = vi.fn();
    render(
      <NotificationsView
        channels={[make()]}
        onSetEnabled={onSetEnabled}
        togglingIds={new Set(["1"])}
        onSave={async () => {}}
      />,
    );
    const button = screen.getByRole("button", {
      name: "Disable Slack On-call Slack",
    });
    expect(button.hasAttribute("disabled")).toBe(true);
    fireEvent.click(button);
    expect(onSetEnabled).not.toHaveBeenCalled();
  });

  it("offers no toggle at all to an account that may not write", () => {
    render(<NotificationsView channels={[make()]} onSave={async () => {}} />);
    expect(screen.queryByRole("button", { name: /disable/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /enable/i })).toBeNull();
  });
});
