// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render as renderBare,
  screen,
  within,
} from "@testing-library/react";
import type { ReactElement } from "react";
import { setToolbarSlot, setTopbarSlot } from "../shell/topbarSlot";
import { NotificationsView } from "./NotificationsView";
import { channelFromApi } from "./channels";
import type { Channel } from "./channels";
import { inventoryFromApi } from "../monitors/inventory";

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

/*
 * A stand-in masthead slot, so the screen's filter field has somewhere to
 * portal to (SUB-138). `TopbarTools` renders nothing when no slot is
 * registered, which is correct behaviour — the status wall has no chrome —
 * but it means a test file that never registers one cannot see the filter at
 * all, and an assertion about filtering would fail for an absence the product
 * does not have. A bare node rather than the real `Topbar`: these tests are
 * about channels, and mounting the shell would let a change to the theme
 * toggle fail one.
 */
function render(ui: ReactElement) {
  const masthead = document.createElement("div");
  const toolbar = document.createElement("div");
  document.body.append(masthead, toolbar);
  setTopbarSlot(masthead);
  setToolbarSlot(toolbar);
  return renderBare(ui);
}

afterEach(() => {
  cleanup();
  // Otherwise the next test portals into the previous test's detached slot,
  // and its controls are rendered into a node nobody can query.
  setTopbarSlot(null);
  setToolbarSlot(null);
});

describe("NotificationsView", () => {
  it("says plainly when unrouted monitors reach nobody", () => {
    /*
     * With no default, a monitor without channels alerts nobody. That is the
     * failure the default exists to prevent, so the page says it in those
     * words rather than leaving the reader to infer it from an empty select.
     */
    render(<NotificationsView channels={[make()]} />);
    expect(
      screen.getByText(/monitors with no channels of their own alert nobody/i),
    ).toBeTruthy();
  });

  it("marks the default channel on its row and names it above the list", () => {
    const { container } = render(
      <NotificationsView
        channels={[make({ is_default: true }), make({ id: 2, name: "Pager" })]}
      />,
    );
    const [first, second] = screen.getAllByRole("listitem");
    expect(within(first).getByText("Default")).toBeTruthy();
    expect(within(second).queryByText("Default")).toBeNull();
    expect(first.querySelector(".sr-only")!.textContent).toMatch(
      /default channel/i,
    );
    expect(container.querySelector(".nt-default")!.textContent).toMatch(
      /alert through On-call Slack/,
    );
  });

  it("says a disabled default delivers nothing", () => {
    // The notifier skips disabled channels, so naming a disabled default as
    // where alerts go would claim a delivery that never happens.
    const { container } = render(
      <NotificationsView
        channels={[make({ is_default: true, enabled: false })]}
      />,
    );
    expect(container.querySelector(".nt-default")!.textContent).toMatch(
      /which is disabled: they alert nobody/,
    );
  });

  it("shows a channel's type and destination", () => {
    render(<NotificationsView channels={[make()]} />);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("On-call Slack")).toBeTruthy();
    expect(within(row).getByText("Slack")).toBeTruthy();
    expect(within(row).getByText("endpoint ending ****B07F")).toBeTruthy();
  });

  it("never claims an untested channel is healthy, and draws no chip for it", () => {
    /*
     * The API carries no delivery history, so a green tick here would be
     * invented — and an invented green tick on a monitoring tool is how a dead
     * channel goes on looking fine for three days.
     *
     * This used to assert a dashed "Not verified" chip on the row. The chip is
     * gone (SUB-138) because it was on 100% of rows, always, and structurally
     * incapable of differing: a value that cannot vary is not information, and
     * that one was taking the heaviest ink in the row to be none. What must
     * not change is the claim, so the assertion is now in two halves — the row
     * states nothing about delivery, and the list states the reason once.
     */
    const { container } = render(<NotificationsView channels={[make()]} />);
    const row = screen.getByRole("listitem");
    // No status chip on the row: not green, not red, not any colour at all.
    expect(row.querySelector(".chip--status")).toBeNull();
    expect(row.querySelector(".chip--state")).toBeNull();
    /*
     * Nothing about delivery in the row's *visible* text. The `sr-only`
     * sentence deliberately still says "Not verified" and is excluded here:
     * the eye has the list's legend a few centimetres above and in view, and
     * a screen-reader user moving item by item through a list does not, so
     * dropping it there would take the caveat away from the one reader who
     * cannot see it stated once.
     */
    const visible = [...row.childNodes]
      .map((node) => (node as HTMLElement).textContent ?? "")
      .join(" ");
    const srOnly = row.querySelector(".sr-only")!.textContent ?? "";
    const seen = visible.replace(srOnly, "");
    expect(seen).not.toMatch(/delivered/i);
    expect(seen).not.toMatch(/verified/i);
    expect(srOnly).toMatch(/Not verified/);
    /*
     * And the sentence that replaces it is present and unhedged. "Keeps no
     * delivery history" is the load-bearing phrase: it is a statement about
     * what SubGlance can see, not a reassurance, and it is in the summary
     * rather than behind the disclosure so it is read without a click.
     */
    const summary = container.querySelector(".nt-legend-summary");
    expect(summary).not.toBeNull();
    expect(summary!.textContent).toMatch(/keeps no delivery history/i);
  });

  it("draws a delivery chip only once a test has produced a result", () => {
    /*
     * The other half of the same decision. Removing the always-on chip must
     * not remove the chip: a real result is per-row information and is exactly
     * what the row should carry. A page that dropped both would be quieter and
     * less honest.
     */
    render(
      <NotificationsView
        channels={[make(), make({ id: 2, name: "Pager" })]}
        deliveries={{ "1": { kind: "passed" } }}
      />,
    );
    const [tested, untested] = screen.getAllByRole("listitem");
    expect(
      within(tested).getByText("Test delivered", { selector: ".chip" }),
    ).toBeTruthy();
    expect(untested.querySelector(".chip--status")).toBeNull();
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
    expect(
      screen.getByRole("button", { name: "Edit Slack On-call Slack" }),
    ).toBeTruthy();
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
    expect(
      within(row).getByText("Test failed", { selector: ".chip" }),
    ).toBeTruthy();
  });

  it("does not let a passed test claim anything about earlier deliveries", () => {
    render(
      <NotificationsView
        channels={[make()]}
        deliveries={{ "1": { kind: "passed" } }}
      />,
    );
    expect(
      screen.getByText(/says nothing about deliveries made before/i),
    ).toBeTruthy();
  });

  it("states the consequence when there are no channels at all", () => {
    // Not a polite grey murmur: no channel means every alert goes into a void
    // on an install that otherwise looks healthy.
    render(
      <NotificationsView
        channels={[]}
        onCreateOpenChange={() => {}}
        onSave={async () => {}}
      />,
    );
    expect(screen.getByText("Alerts are going nowhere.")).toBeTruthy();
  });

  it("never shows the empty state for a failed load", () => {
    // "Alerts are going nowhere" to somebody with four working channels tells
    // them their alerting is gone when in fact one request 500'd.
    render(<NotificationsView channels={[]} error={new Error("HTTP 500")} />);
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
    expect(screen.getByText("HTTP 500")).toBeTruthy();
  });

  it("never shows the empty state while still loading", () => {
    render(<NotificationsView channels={[]} loading />);
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
    expect(screen.getAllByText(/loading channels/i).length).toBeGreaterThan(0);
  });

  it("states no count while the channels are still loading", () => {
    /*
     * The defect this page was reopened for (SUB-138). `channels` is `[]`
     * before the first response resolves, and the card's note was computed
     * from it unconditionally — so the screen rendered "No channels
     * configured" directly above its own "Loading channels…", stating a fact
     * it had no way to know and picking the most alarming of the two
     * possibilities to be confident about.
     *
     * Asserted as "the note says nothing at all", not as "the note does not
     * say zero": any placeholder count here is the same lie with different
     * wording.
     */
    const { container } = render(<NotificationsView channels={[]} loading />);
    expect(screen.getAllByText(/loading channels/i).length).toBeGreaterThan(0);
    expect(container.querySelector(".card-title")!.textContent).toBe(
      "Channels",
    );
    expect(container.querySelector(".card-note")).toBeNull();
    expect(screen.queryByText(/no channels configured/i)).toBeNull();
  });

  it("states no count while the load is failing either", () => {
    const { container } = render(
      <NotificationsView channels={[]} error={new Error("HTTP 500")} />,
    );
    expect(container.querySelector(".card-title")!.textContent).toBe(
      "Channels",
    );
    expect(container.querySelector(".card-note")).toBeNull();
    expect(screen.queryByText(/no channels configured/i)).toBeNull();
  });

  it("counts the channels in the heading once they have actually arrived", () => {
    /*
     * The other half of the rule: bare while unknown, and a real count the
     * moment there is one. A heading that never gained its count would pass
     * the two tests above and lose the page its count entirely.
     *
     * `Channels (2)` rather than a separate "2 channels" line (round-2
     * review), and the same form the Monitors screen uses, so the two screens
     * are read the same way.
     */
    const { container } = render(
      <NotificationsView
        channels={[make(), make({ id: 2, enabled: false })]}
      />,
    );
    expect(container.querySelector(".card-title")!.textContent).toBe(
      "Channels (2)",
    );
    // The count is in the heading, not on a line of its own beneath it.
    expect(container.querySelector(".card-note")).toBeNull();
  });

  it("puts exactly one h1 on the page, with the card a level under it", () => {
    /*
     * `Card` renders `headingLevel` as a real heading element, so
     * `headingLevel={1}` on the card produced a second `h1`: the sr-only one
     * naming the route, and the card's naming a section inside it (CodeRabbit,
     * PR #61). Two level-one headings is not an outline, and it breaks heading
     * navigation for exactly the reader the sr-only heading was added for —
     * "the page" and "a card on it" stop being distinguishable.
     *
     * Asserted on both states, because the card's title is conditional and a
     * regression could reach only one of them.
     */
    for (const channels of [[], [make(), make({ id: 2 })]]) {
      const { container, unmount } = render(
        <NotificationsView channels={channels} />,
      );
      const h1s = [...container.querySelectorAll("h1")];
      expect(h1s.map((h) => h.textContent)).toEqual(["Notifications"]);
      const title = container.querySelector(".card-title")!;
      expect(title.tagName).toBe("H2");
      unmount();
    }
  });

  it("keeps the disabled channels findable once the count line is gone", () => {
    /*
     * "2 channels, 1 disabled" carried a real fact and `Channels (2)` does
     * not carry it, so it has to survive somewhere or this is a silent
     * deletion. It survives on the rows: every disabled channel wears a
     * `Disabled` chip beside its name.
     *
     * That is strictly more than the header line said. "1 disabled" told you
     * a channel was silent and left you to work out which; the chip names it.
     * Asserted as "one chip per disabled channel, and none on an enabled
     * one", which is what makes it a substitute rather than a coincidence.
     */
    const { container } = render(
      <NotificationsView
        channels={[
          make({ id: 1, name: "Enabled one" }),
          make({ id: 2, name: "Quiet one", enabled: false }),
        ]}
      />,
    );
    const rows = [...container.querySelectorAll(".inv-row")];
    const disabledRows = rows.filter(
      (row) => row.getAttribute("data-disabled") === "true",
    );
    expect(disabledRows).toHaveLength(1);
    expect(disabledRows[0].textContent).toContain("Quiet one");
    /*
     * The visible chip, not the row's text. Every row carries an `sr-only`
     * sentence that already ends "Enabled." or "Disabled.", so asserting on
     * `textContent` passes with the chip deleted — the eye would lose the
     * fact while the assertion went on being satisfied by the screen-reader
     * copy. The chip element is the thing that has to be there.
     */
    const chip = disabledRows[0].querySelector(".inv-paused-chip");
    expect(
      chip,
      "the disabled row lost its visible Disabled chip",
    ).not.toBeNull();
    expect(chip!.textContent).toBe("Disabled");
    const enabledRow = rows.find(
      (row) => row.getAttribute("data-disabled") === "false",
    )!;
    expect(enabledRow.querySelector(".inv-paused-chip")).toBeNull();
  });

  it("does not say alerts are going nowhere when a filter matched nothing", () => {
    /*
     * A search box must not be able to fire the page's most alarming
     * sentence. Previously "no visible channels" and "no channels" were one
     * branch, so typing a non-matching query on a healthy instance announced
     * that every alert was going into a void.
     */
    render(
      <NotificationsView
        channels={[make(), make({ id: 2, name: "Ops mail", type: "email" })]}
        onCreateOpenChange={() => {}}
        onSave={async () => {}}
      />,
    );
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "zzzz" },
    });
    expect(screen.queryByText("Alerts are going nowhere.")).toBeNull();
    expect(screen.getByText(/no channel matches/i).textContent).toContain(
      "2 channels are configured",
    );
  });

  it("offers exactly one way to add a channel when there are none", () => {
    /*
     * The empty state is the one screen that must present a single obvious
     * next step, and it was presenting two identical primary buttons — the
     * card header's "Add channel" and the body's "Add a channel" — a few
     * centimetres apart.
     */
    render(
      <NotificationsView
        channels={[]}
        onCreateOpenChange={() => {}}
        onSave={async () => {}}
      />,
    );
    expect(
      screen.getAllByRole("button", { name: /add (a )?channel/i }),
    ).toHaveLength(1);
  });

  it("keeps the header's add button on a list filtered down to nothing", () => {
    // The suppression above is for an empty instance only: with channels that
    // the filter hid, the header button is the only way to add one.
    render(
      <NotificationsView
        channels={[make()]}
        onCreateOpenChange={() => {}}
        onSave={async () => {}}
      />,
    );
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "zzzz" },
    });
    expect(screen.getByRole("button", { name: "Add channel" })).toBeTruthy();
  });

  it("says what a channel is, not only what happens without one", () => {
    // The empty state is the first thing a new self-hoster reads, and it used
    // to assume they already knew what they were being asked to add.
    render(
      <NotificationsView
        channels={[]}
        onCreateOpenChange={() => {}}
        onSave={async () => {}}
      />,
    );
    expect(
      screen.getByText(/a channel is where SubGlance sends a message/i),
    ).toBeTruthy();
    // The supported types, from CHANNEL_TYPES rather than from a hand-written
    // list that could advertise a type the store's CHECK constraint rejects.
    expect(
      screen.getByText(/Email, Slack, Discord, Telegram, Webhook/),
    ).toBeTruthy();
  });

  it("keeps the whole delivery caveat on the page, above the rows it explains", () => {
    /*
     * The honesty rule (SUB-55): this text is why no row ever says
     * "delivered", and it may be moved, re-weighted or folded into a
     * disclosure but never dropped. That is the distinction this test
     * enforces — the previous block was rejected for its *weight*, not its
     * content, and the easy way to satisfy that rejection is to delete a
     * sentence. Asserted on the load-bearing clauses so a reword survives and
     * a quiet deletion does not.
     */
    const { container } = render(<NotificationsView channels={[make()]} />);
    const legend = container.querySelector(".nt-legend");
    expect(legend).not.toBeNull();
    expect(legend!.textContent).toMatch(/carries no delivery history/i);
    expect(legend!.textContent).toMatch(
      /failed every delivery for three days looks exactly the same/i,
    );
    // Above the list, not after it: it explains the rows that follow.
    expect(
      legend!.compareDocumentPosition(container.querySelector(".inv-list")!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("states the caveat without needing the disclosure opened", () => {
    /*
     * A disclosure is only an acceptable home for a caveat if the part that is
     * always visible already makes the claim. Folding "nothing here is known
     * to work" behind a click would be silencing it, which SUB-55 forbids —
     * what is behind the click is the *reason*, not the fact.
     *
     * So: the summary alone, with the details element closed, has to carry the
     * statement. And the element must genuinely start closed, or this is the
     * rejected six-line block wearing a triangle.
     */
    const { container } = render(<NotificationsView channels={[make()]} />);
    const details = container.querySelector(".nt-legend") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    const summary = container.querySelector(".nt-legend-summary")!;
    expect(summary.textContent).toMatch(/keeps no delivery history/i);
    /*
     * And it must not overreach into denying a result the rows can carry.
     * "No channel below is known to be working" contradicted a row that had
     * just passed a test and says "Test delivered" (CodeRabbit, PR #61): the
     * two sentences appeared on the same screen the moment anyone pressed
     * Send test. The caveat SUB-55 requires is the missing *history*, which
     * is true whatever any row says.
     */
    expect(summary.textContent).not.toMatch(/known to be working/i);
  });

  it("gives the delivery caveat less prose than the list it qualifies", () => {
    /*
     * The rejection, as an assertion. On the owner's instance the caveat was a
     * six-line block above two rows — you read an explanation of a column
     * before reaching the column. jsdom has no layout, so height cannot be
     * measured here; what can be measured is the thing that produced the
     * height, which is how much prose is on screen before the first row.
     *
     * Two channels, matching his instance. The always-visible text above the
     * list must be shorter than the rows themselves. This is deliberately a
     * weak bound — it does not dictate a design, it only fails the shape that
     * was rejected — and `channel-row.browser.test.ts` measures the real
     * geometry in a real engine.
     */
    const { container } = render(
      <NotificationsView
        channels={[make(), make({ id: 2, name: "Weekend pager" })]}
      />,
    );
    const summary = container.querySelector(".nt-legend-summary")!;
    const list = container.querySelector(".inv-list")!;
    const visibleCaveat = (summary.textContent ?? "").trim().length;
    const rows = (list.textContent ?? "").trim().length;
    expect(visibleCaveat).toBeLessThan(rows);
  });

  it("gives the empty state two surfaces, never three", () => {
    /*
     * PR #57 removed exactly this from the add drawer — "drawer title, card
     * title and a heading inside the form were three names for one thing" —
     * and it came back here as "Channels" / "No channels configured" /
     * "Alerts are going nowhere.", stacked, on the first screen a new
     * self-hoster ever sees.
     *
     * The card's title is one surface and the body's headline is the other.
     * The count in between is the one that goes, because a count of zero is
     * the headline's own fact said first and worse.
     */
    const { container } = render(
      <NotificationsView channels={[]} onSave={async () => {}} />,
    );
    expect(container.querySelector(".card-note")).toBeNull();
    expect(screen.queryByText("No channels configured")).toBeNull();
    // Both surfaces that should be there, still are — and the heading is bare
    // rather than reading "Channels (0)", which is the same count said in the
    // one place the headline below is about to say it better.
    expect(container.querySelector(".card-title")!.textContent).toBe(
      "Channels",
    );
    expect(screen.getByText("Alerts are going nowhere.")).toBeTruthy();
  });

  it("draws the destructive row action as a bin, quietly, like the monitors row", () => {
    /*
     * The criticism this answers: four full outlined buttons per row with
     * Delete in red on every one of them, on a screen whose subject is the two
     * lines of text to their left. The monitors inventory had already solved
     * it — small square icon buttons, the bin carrying "destructive" in its
     * shape so it survives greyscale — and this page ignored the precedent.
     *
     * Asserted against the inventory's own classes rather than against new
     * ones, because the point is that the two lists share a vocabulary. A
     * reimplementation that looked identical would pass a screenshot and fail
     * this.
     */
    const { container } = render(
      <NotificationsView
        channels={[make()]}
        onTest={() => {}}
        onSetEnabled={() => {}}
        onDelete={() => {}}
        onSave={async () => {}}
      />,
    );
    const del = screen.getByRole("button", {
      name: "Delete Slack On-call Slack",
    });
    expect(del.className).toContain("inv-act--icon");
    expect(del.className).toContain("inv-act--danger");
    // A glyph, not a word: the accessible name carries the verb, the face does not.
    expect((del.textContent ?? "").trim()).toBe("");
    expect(del.querySelector("svg")).not.toBeNull();
    // Edit and the toggle are glyphs on the same class.
    for (const name of [
      "Edit Slack On-call Slack",
      "Disable Slack On-call Slack",
    ]) {
      const button = screen.getByRole("button", { name });
      expect(button.className).toContain("inv-act--icon");
      expect((button.textContent ?? "").trim()).toBe("");
    }
    /*
     * Send test is the deliberate exception and stays a word. It is the page's
     * primary verb, the one action not in the monitors row's vocabulary, and
     * it sends a real message to somebody else's inbox — not something to put
     * behind an unlabelled square.
     */
    const test = container.querySelector(".nt-act-test")!;
    expect(test.textContent).toBe("Send test");
    expect(test.className).not.toContain("inv-act--icon");
  });

  it("reserves the width of the test button, whose label changes", () => {
    /*
     * The class is asserted here; the geometry it buys is asserted in
     * `channel-row.browser.test.ts`, which has a layout engine. jsdom reports
     * every width as zero, so a test here claiming the columns line up would
     * pass against a stylesheet that does nothing — which is the exact failure
     * mode that let the misalignment ship in the first place.
     *
     * Only the test button now. The toggle's reservation existed because
     * "Enable" and "Disable" are different widths; it is a fixed square glyph
     * (SUB-138), so there is nothing left to reserve and the rule that did it
     * was deleted rather than left pointing at a token.
     */
    const { container } = render(
      <NotificationsView
        channels={[make()]}
        onTest={() => {}}
        onSetEnabled={() => {}}
      />,
    );
    expect(container.querySelector(".nt-act-test")).not.toBeNull();
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
    expect(
      screen.getByText(/will go on being checked and will tell nobody/i),
    ).toBeTruthy();

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
    /*
     * The button is a glyph now (SUB-138), so the in-flight state has no word
     * on its face and is announced instead: the accessible name says
     * "Saving…" and `aria-busy` is what assistive technology hears. Asserted
     * on the name rather than on `textContent`, which is empty by design.
     */
    const button = screen.getByRole("button", {
      name: "Saving… Slack On-call Slack",
    });
    expect(button.getAttribute("aria-busy")).toBe("true");
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

describe("who hears what", () => {
  /*
   * SUB-124: "who hears about this monitor" must be answerable in one glance,
   * without evaluating default-plus-override and disabled channels in one's
   * head. The finding is the monitor that alerts nobody, so that is what has
   * to be on screen without opening anything.
   */
  function mon(id: number, name: string, over: Record<string, unknown> = {}) {
    return inventoryFromApi({
      id,
      name,
      type: "http",
      target: "https://example.com",
      interval_s: 60,
      timeout_s: 10,
      enabled: true,
      status: "up",
      created_at: "2026-09-01T10:00:00Z",
      channels: [],
      ...over,
    } as Parameters<typeof inventoryFromApi>[0]);
  }

  it("lists a monitor that alerts nobody in the open, with the reason", () => {
    render(
      <NotificationsView
        channels={[make({ enabled: false })]}
        monitors={[
          mon(1, "billing", { channels: [{ id: 1, name: "On-call Slack" }] }),
          mon(2, "cdn", { channels: [{ id: 1, name: "On-call Slack" }] }),
        ]}
      />,
    );
    expect(screen.getByText("2 active monitors alert nobody.")).toBeTruthy();
    const silent = screen.getByRole("list", {
      name: "Monitors that alert nobody",
    });
    expect(within(silent).getByText("billing")).toBeTruthy();
    expect(
      within(silent).getAllByText("nobody: On-call Slack (disabled)"),
    ).toHaveLength(2);
  });

  it("says every monitor is covered when the default reaches them", () => {
    render(
      <NotificationsView
        channels={[make({ is_default: true })]}
        monitors={[mon(1, "billing", { default_channel: { id: 1, name: "On-call Slack" } })]}
      />,
    );
    expect(
      screen.getByText("Every active monitor reaches at least one channel."),
    ).toBeTruthy();
    expect(
      screen.queryByRole("list", { name: "Monitors that alert nobody" }),
    ).toBeNull();
    expect(screen.getByText("On-call Slack (default)")).toBeTruthy();
  });

  it("does not give the all-clear while a monitor's coverage is unknown", () => {
    // The channel list knows a default the inventory does not carry yet, so
    // this monitor's route is unknown: neither a finding nor confirmed.
    render(
      <NotificationsView
        channels={[make({ is_default: true })]}
        monitors={[mon(1, "billing")]}
      />,
    );
    expect(
      screen.queryByText("Every active monitor reaches at least one channel."),
    ).toBeNull();
    expect(
      screen.getByText(
        "No active monitor is known to alert nobody, but 1 could not be checked yet.",
      ),
    ).toBeTruthy();
  });

  it("announces the coverage sentence, not the monitor list", () => {
    render(
      <NotificationsView
        channels={[make({ enabled: false })]}
        monitors={[mon(1, "billing", { channels: [{ id: 1, name: "On-call Slack" }] })]}
      />,
    );
    const status = screen
      .getAllByRole("status")
      .find((el) => el.textContent === "1 active monitor alerts nobody.");
    expect(status).toBeTruthy();
    expect(within(status!).queryByText("billing")).toBeNull();
  });

  it("does not turn a failed monitor list into 'nobody hears'", () => {
    render(<NotificationsView channels={[make()]} monitors={null} monitorsFailed />);
    expect(screen.getByText(/could not be loaded, so who hears/i)).toBeTruthy();
    expect(screen.queryByText(/active monitors? alerts? nobody/i)).toBeNull();
  });

  it("waits for the channel list before judging any monitor", () => {
    // Without the channels a disabled route cannot be told from a live one.
    render(
      <NotificationsView
        channels={[]}
        loading
        monitors={[mon(1, "billing")]}
      />,
    );
    expect(screen.getByText("Loading monitors…")).toBeTruthy();
    expect(screen.queryByText(/active monitors? alerts? nobody/i)).toBeNull();
  });
});
