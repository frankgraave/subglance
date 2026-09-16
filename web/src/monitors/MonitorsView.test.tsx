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
import { MonitorsView } from "./MonitorsView";
import { setTopbarSlot } from "../shell/topbarSlot";
import { inventoryFromApi } from "./inventory";
import type { InventoryMonitor } from "./inventory";

/*
 * The inventory screen, rendered from fixtures.
 *
 * Every assertion here is about a claim the screen makes, not about markup:
 * whether a paused monitor says so in words, whether an unloaded channel cell
 * can be mistaken for an empty one, whether a control that does nothing is
 * still offered, and whether an unrecorded check admits it. Those are the
 * things that make this page trustworthy or not.
 */

function make(over: Record<string, unknown> = {}): InventoryMonitor {
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

/**
 * Renders the screen with a toolbar to put its controls in.
 *
 * Since SUB-134 the search box, the two filters and the "n of m shown" counter
 * are portalled into the shell's toolbar instead of sitting in a band inside
 * the Card. That is the behaviour under test in several assertions below, and
 * a bare `render` has no toolbar — so the controls would have nowhere to go
 * and every assertion about them would fail for an absence the product does
 * not have.
 *
 * A stand-in slot rather than mounting the real `Topbar`: what these tests are
 * about is the inventory, and dragging the whole shell in would make a change
 * to the theme toggle able to fail a test about type filters. `App.test.tsx`
 * is where the two are asserted together.
 */
function render(ui: ReactElement) {
  const slot = document.createElement("div");
  document.body.append(slot);
  setTopbarSlot(slot);
  return renderBare(ui);
}

afterEach(() => {
  cleanup();
  // Otherwise the next test portals into the previous test's detached slot,
  // and its controls are rendered into a node nobody can query.
  setTopbarSlot(null);
});

describe("MonitorsView", () => {
  it("shows the settings the dashboard refuses to show", () => {
    render(<MonitorsView monitors={[make()]} />);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Type")).toBeTruthy();
    expect(within(row).getByText("HTTP")).toBeTruthy();
    expect(within(row).getByText("Every")).toBeTruthy();
    expect(within(row).getByText("1 min")).toBeTruthy();
    expect(within(row).getByText("Timeout")).toBeTruthy();
    expect(within(row).getByText("10 s")).toBeTruthy();
    expect(within(row).getByText("Channels")).toBeTruthy();
  });

  it("shows paused monitors by default, where the dashboard hides them", () => {
    // This is the strongest argument for a separate page: the two views want
    // opposite defaults, and a monitor somebody paused during a deploy and
    // forgot is exactly what an inventory is opened to find.
    render(
      <MonitorsView
        monitors={[make(), make({ id: 2, name: "cdn", enabled: false })]}
      />,
    );
    expect(screen.getByText("cdn")).toBeTruthy();
    expect(screen.getByText("2 configured, 1 paused")).toBeTruthy();
  });

  it("says Paused in words, not only by dimming the row", () => {
    render(<MonitorsView monitors={[make({ enabled: false })]} />);
    // Inside the row, so the "Paused" label on the filter dropdown cannot
    // stand in for a signal the row itself is supposed to carry.
    // The chip, specifically — not the lamp's hidden word and not the filter
    // dropdown. A sighted reader who cannot see the dimming needs the word on
    // the row itself.
    const row = screen.getByRole("listitem");
    expect(
      within(row).getByText("Paused", { selector: ".chip" }),
    ).toBeTruthy();
  });

  it("distinguishes 'no channels' from 'channels not loaded'", () => {
    // The cell that looks empty means two completely different things, and
    // only one of them is a misconfiguration worth chasing.
    render(
      <MonitorsView
        monitors={[make(), make({ id: 2, name: "cdn" })]}
        channels={{ "1": { known: true, names: [] } }}
      />,
    );
    const [first, second] = screen.getAllByRole("listitem");
    // Scoped to the channels column: the tags column also says "none", and
    // the point here is which column is telling the truth about what we know.
    const channelCell = first.querySelector(".inv-col--chan") as HTMLElement;
    expect(within(channelCell).getByText("none")).toBeTruthy();
    expect(within(first).queryByText("not loaded")).toBeNull();
    expect(within(second).getByText("not loaded")).toBeTruthy();
  });

  it("explains a truncated channel fan-out rather than leaving 'not loaded' unexplained", () => {
    render(
      <MonitorsView monitors={[make()]} channelsTruncated />,
    );
    expect(
      screen.getByText(/the page stopped\s+asking, it did not find out/i),
    ).toBeTruthy();
  });

  it("disables Check now for a push monitor and says why", () => {
    render(
      <MonitorsView
        monitors={[
          make({ id: 3, name: "backup", type: "push", target: "", push_interval_s: 86400 }),
        ]}
        onCheckNow={() => {}}
      />,
    );
    const button = screen.getByRole("button", {
      name: /check now backup — unavailable for a push monitor/i,
    });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it("allows Check now on a paused monitor", () => {
    const onCheckNow = vi.fn();
    render(
      <MonitorsView
        monitors={[make({ enabled: false })]}
        onCheckNow={onCheckNow}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Check now auth" }));
    expect(onCheckNow).toHaveBeenCalledWith("1");
  });

  it("admits when a manual check was not recorded", () => {
    // A green result that did not move the dashboard otherwise reads as a bug
    // in the dashboard.
    render(
      <MonitorsView
        monitors={[make({ enabled: false })]}
        onCheckNow={() => {}}
        checkResults={{ "1": { ok: true, latencyMs: 12, recorded: false } }}
      />,
    );
    expect(screen.getByText(/Not recorded/)).toBeTruthy();
  });

  it("does not say 'not recorded' about a check that was recorded", () => {
    render(
      <MonitorsView
        monitors={[make()]}
        onCheckNow={() => {}}
        checkResults={{ "1": { ok: true, latencyMs: 12, recorded: true } }}
      />,
    );
    expect(screen.queryByText(/Not recorded/)).toBeNull();
  });

  it("names the monitor in every row action, so the buttons are distinguishable", () => {
    // Four rows of identically-labelled "Pause" buttons is a list a screen
    // reader cannot navigate: the name is part of each accessible name.
    render(
      <MonitorsView
        monitors={[make(), make({ id: 2, name: "cdn" })]}
        onTogglePaused={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "Pause auth" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Pause cdn" })).toBeTruthy();
  });

  it("offers Resume rather than Pause on a paused monitor", () => {
    render(
      <MonitorsView
        monitors={[make({ enabled: false })]}
        onTogglePaused={() => {}}
      />,
    );
    expect(screen.getByRole("button", { name: "Resume auth" })).toBeTruthy();
  });

  it("asks before deleting, and names what goes with it", () => {
    const onDelete = vi.fn();
    render(<MonitorsView monitors={[make()]} onDelete={onDelete} />);
    fireEvent.click(screen.getByRole("button", { name: "Delete auth" }));
    // Not a one-click destroy: deleting takes the heartbeats, the uptime
    // history and the incident record with it, which the button does not say.
    expect(onDelete).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).getByText(/heartbeats, uptime history and past incidents/),
    ).toBeTruthy();
    fireEvent.click(
      within(dialog).getByRole("button", { name: /^Delete auth$/ }),
    );
    expect(onDelete).toHaveBeenCalledWith("1");
  });

  it("offers no write controls at all to an account that may not write", () => {
    // A control that exists and then fails with a 403 is worse than no
    // control: it tells the user their instance is broken.
    render(<MonitorsView monitors={[make()]} />);
    expect(screen.queryByRole("button", { name: /pause/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /delete/i })).toBeNull();
    expect(screen.getByText("read only")).toBeTruthy();
  });

  it("opens the create drawer when the route asked for it", () => {
    render(
      <MonitorsView
        monitors={[make()]}
        createOpen
        onCreateOpenChange={() => {}}
      />,
    );
    expect(
      screen.getByRole("dialog", { name: /add monitor/i }),
    ).toBeTruthy();
  });

  it("filters by type without losing the count of what is hidden", () => {
    render(
      <MonitorsView
        monitors={[make(), make({ id: 2, name: "db", type: "tcp", target: "db:5432" })]}
      />,
    );
    fireEvent.change(screen.getByRole("combobox", { name: /type/i }), {
      target: { value: "tcp" },
    });
    expect(screen.queryByText("auth")).toBeNull();
    expect(screen.getByText("1 of 2 shown")).toBeTruthy();
  });

  it("never shows the empty state for a list that failed to load", () => {
    // "Nothing is being watched yet" on an instance with forty monitors is the
    // most alarming wrong thing this screen can say: it reads as a lost
    // configuration when in fact one request 500'd.
    render(<MonitorsView monitors={[]} error={new Error("HTTP 500")} />);
    expect(screen.queryByText(/Nothing is being watched yet/)).toBeNull();
    expect(screen.getByText(/could not be loaded/)).toBeTruthy();
  });

  it("never shows the empty state while the list is still loading", () => {
    render(<MonitorsView monitors={[]} loading />);
    expect(screen.queryByText(/Nothing is being watched yet/)).toBeNull();
  });

  it("shows a failed write on the row it belongs to", () => {
    render(
      <MonitorsView
        monitors={[make()]}
        onTogglePaused={() => {}}
        rowErrors={{ "1": "monitor 1 was modified by someone else" }}
      />,
    );
    expect(
      screen.getByText("monitor 1 was modified by someone else"),
    ).toBeTruthy();
  });
});

/**
 * SUB-134: the row actions became glyphs, and their names had to survive it.
 *
 * This is the failure mode icon buttons ship with. The visible word used to be
 * the accessible name's prefix (WCAG 2.5.3); with the word gone, the only
 * thing standing between forty rows and forty buttons called "Pause" is an
 * `aria-label` nobody can see. A rename that drops the monitor's name breaks
 * screen-reader navigation and voice control at once and looks like nothing on
 * screen, which is why it is asserted per action rather than spot-checked.
 *
 * Two monitors on purpose: with one, "Pause" and "Pause auth" are equally
 * unambiguous and the test would pass against the very defect it exists for.
 */
describe("every row action names the monitor it acts on", () => {
  const ACTIONS = [/check now/i, /pause/i, /edit/i, /delete/i];

  function inventory() {
    return render(
      <MonitorsView
        monitors={[make(), make({ id: 2, name: "cdn" })]}
        onCheckNow={() => {}}
        onTogglePaused={() => {}}
        onEdit={() => {}}
        onDelete={() => {}}
        onSave={async () => {}}
      />,
    );
  }

  it("carries the name in the accessible name of all four", () => {
    inventory();
    for (const action of ACTIONS) {
      const buttons = screen.getAllByRole("button", { name: action });
      const names = buttons.map((b) => b.getAttribute("aria-label") ?? b.textContent ?? "");
      // One per monitor, and no two of them are the same string.
      expect(names).toHaveLength(2);
      expect(new Set(names).size).toBe(2);
      expect(names.some((n) => n.includes("auth"))).toBe(true);
      expect(names.some((n) => n.includes("cdn"))).toBe(true);
    }
  });

  it("keeps Delete readable without colour", () => {
    /*
     * DESIGN.md §2.3: colour is never the only carrier. The other three
     * actions are glyphs and Delete is not, because a red bin beside three
     * grey glyphs is a grey bin to a reader who cannot see the red — and this
     * is the one action that cannot be undone.
     *
     * Asserted as "the button contains the word" rather than as "it has no
     * icon", so adding a glyph BESIDE the word stays allowed while replacing
     * the word with one does not.
     */
    inventory();
    for (const button of screen.getAllByRole("button", { name: /delete/i })) {
      expect(button.textContent).toContain("Delete");
    }

    // And the three that did become glyphs really are glyphs: if they kept
    // their words, this file would be asserting nothing about icon buttons.
    for (const action of [/check now/i, /pause/i, /edit/i]) {
      for (const button of screen.getAllByRole("button", { name: action })) {
        expect(button.textContent?.trim()).toBe("");
        expect(button.querySelector("svg")).not.toBeNull();
      }
    }
  });
});
