// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IncidentsView } from "./IncidentsView";
import type { Incident } from "../monitors/detail";

/**
 * The incidents screen's list header (SUB-136, SUB-207).
 *
 * The controls stand at the head of the card they narrow: All, Open and
 * Resolved as tabs with their counts on the left, the monitor filter and the
 * history window on the right. The rule under test is the one SUB-136 states
 * and the move kept: **every control in the header does what it says, and a
 * control whose wiring is absent is not rendered at all.**
 */

const T0 = 1_700_000_000_000;
const NOW = T0 + 720_000;

const incident = (over: Partial<Incident> = {}): Incident => ({
  id: "1",
  monitorId: "7",
  startedAt: T0,
  confirmedAt: T0 + 60_000,
  resolvedAt: null,
  ackedAt: null,
  confirmed: true,
  resolved: false,
  acked: false,
  durationS: 720,
  cause: "dns",
  lastError: "lookup api.example.com: no such host",
  ...over,
});

const resolvedIncident = incident({
  id: "r1",
  monitorId: "8",
  resolved: true,
  resolvedAt: T0 + 600_000,
});

afterEach(() => {
  cleanup();
});

/** The card's header, where every control of this screen stands. */
const header = () => document.querySelector<HTMLElement>(".inc-board > .card-head")!;

/** The state tabs, by the word they start with: "Open 1" is the Open tab. */
function tab(word: "All" | "Open" | "Resolved"): HTMLElement {
  const group = within(header()).getByRole("group", { name: "Filter by state" });
  return within(group).getByRole("button", { name: new RegExp(`^${word}\\b`) });
}

/** What a screen reader hears as the list narrows. */
const announced = () =>
  document.querySelector(".inc-board > .sr-only[role='status']")?.textContent ?? "";

describe("the incidents header holds the controls, and everything in it works", () => {
  it("puts the tabs, the filter and the window in the card's header", () => {
    render(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        onHistoryDaysChange={() => {}}
      />,
    );
    const head = header();
    expect(within(head).getByRole("group", { name: "Filter by state" })).toBeTruthy();
    expect(within(head).getByRole("searchbox", { name: "Filter incidents by monitor" })).toBeTruthy();
    expect(within(head).getByRole("button", { name: "History: 30 days" })).toBeTruthy();
    // All first and pressed: the screen shows everything until asked not to.
    expect(tab("All").getAttribute("aria-pressed")).toBe("true");
    expect(tab("Open").getAttribute("aria-pressed")).toBe("false");
  });

  it("scopes to open incidents by hiding the history, not emptying it", () => {
    /*
     * An empty Resolved list under a scope that excludes it would read as
     * "nothing resolved" — a claim about the instance rather than about the
     * filter, and exactly the kind of accidental good news this screen may
     * never print.
     */
    render(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "7": "api", "8": "cdn" }}
      />,
    );
    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();

    fireEvent.click(tab("Open"));

    expect(tab("Open").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("heading", { name: "Open incidents" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Resolved" })).toBeNull();
    expect(document.body.textContent).not.toMatch(/nothing resolved/i);
  });

  it("scopes to resolved by hiding the open list", () => {
    render(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "7": "api", "8": "cdn" }}
      />,
    );

    fireEvent.click(tab("Resolved"));

    expect(screen.queryByRole("heading", { name: "Open incidents" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();
  });

  it("goes back to All when the pressed tab is pressed again", () => {
    render(<IncidentsView incidents={[incident()]} resolved={[resolvedIncident]} now={NOW} />);
    fireEvent.click(tab("Open"));
    fireEvent.click(tab("Open"));
    expect(tab("All").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();
  });

  it("keeps the Resolved list present when the scope asked for it and it is empty", () => {
    /*
     * Asking for resolved and getting nothing back is indistinguishable from
     * the screen having lost the control. The list stays and states its own
     * emptiness, which is the same rule the undated-incident case follows.
     */
    render(<IncidentsView incidents={[incident()]} resolved={[]} now={NOW} />);

    fireEvent.click(tab("Resolved"));

    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();
    expect(document.body.textContent).toMatch(/nothing resolved in the last/i);
  });

  it.each([false, true])("does not call filtered history empty when more pages = %s", (historyHasMore) => {
    const loadMore = vi.fn();
    render(
      <IncidentsView
        incidents={[]}
        resolved={[resolvedIncident]}
        names={{ "8": "cdn" }}
        now={NOW}
        historyHasMore={historyHasMore}
        onLoadMoreHistory={loadMore}
      />,
    );
    fireEvent.click(tab("Resolved"));
    fireEvent.change(within(header()).getByLabelText("Filter incidents by monitor"), {
      target: { value: "api" },
    });
    expect(document.body.textContent).not.toMatch(/nothing resolved in the last/i);
    expect(screen.getByText(/no loaded resolved incidents match/i)).toBeTruthy();
    if (historyHasMore) {
      expect(screen.getByText(/no loaded incidents match/i)).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "Load older incidents" }));
      expect(loadMore).toHaveBeenCalledTimes(1);
      expect(document.body.textContent).toMatch(/more resolved incidents lie inside this window/i);
    }
  });

  it("moves the history window through the owner rather than locally", () => {
    /*
     * The window is a refetch, so the control reports upward instead of
     * filtering what is already on screen. Changing it locally would show a
     * "90 days" label over 30 days of data.
     */
    const onHistoryDaysChange = vi.fn();
    render(
      <IncidentsView
        incidents={[]}
        resolved={[resolvedIncident]}
        now={NOW}
        historyDays={30}
        onHistoryDaysChange={onHistoryDaysChange}
      />,
    );

    fireEvent.click(within(header()).getByRole("button", { name: "History: 30 days" }));
    const panel = screen.getByRole("dialog", { name: "Resolved history window" });
    fireEvent.click(within(panel).getByRole("radio", { name: "90 days" }));

    expect(onHistoryDaysChange).toHaveBeenCalledWith(90);
  });

  it("omits the window control when nothing is listening to it", () => {
    /*
     * The ticket's rule, stated as a test: a control that silently does
     * nothing is worse than an absent one, so a caller with no refetch gets
     * no window. The tabs survive, because scope needs nobody's cooperation.
     */
    render(<IncidentsView incidents={[incident()]} resolved={[]} now={NOW} />);
    expect(within(header()).queryByRole("button", { name: /^History/ })).toBeNull();
    expect(tab("Open")).toBeTruthy();
  });

  it("counts each tab, and the counts do not move with the scope", () => {
    /*
     * The tab's count is what pressing it would show, like the dashboard's
     * status tabs: choosing Open does not turn Resolved into a zero. What is
     * on screen is announced instead.
     */
    render(<IncidentsView incidents={[incident()]} resolved={[resolvedIncident]} now={NOW} />);
    expect(tab("All").textContent).toBe("All 2");
    expect(tab("Open").textContent).toBe("Open 1");
    expect(tab("Resolved").textContent).toBe("Resolved 1");
    expect(announced()).toBe("1 open · 1 resolved");

    fireEvent.click(tab("Open"));
    expect(tab("Resolved").textContent).toBe("Resolved 1");
    expect(announced()).toBe("1 open · 0 resolved");
  });

  it("counts what the filter leaves", () => {
    render(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "7": "api", "8": "cdn" }}
      />,
    );
    fireEvent.change(within(header()).getByLabelText("Filter incidents by monitor"), {
      target: { value: "cdn" },
    });
    expect(tab("All").textContent).toBe("All 1");
    expect(tab("Open").textContent).toBe("Open 0");
    expect(tab("Resolved").textContent).toBe("Resolved 1");
  });

  it.each([
    { historyLoading: true, historyError: null, expected: "loading resolved" },
    { historyLoading: false, historyError: new Error("offline"), expected: "resolved unavailable" },
  ])("reports $expected instead of a resolved count", ({ historyLoading, historyError, expected }) => {
    render(
      <IncidentsView
        incidents={[incident()]}
        resolved={[]}
        now={NOW}
        historyLoading={historyLoading}
        historyError={historyError}
      />,
    );
    // No zero the screen has not measured, on the tab or in the announcement.
    expect(tab("Resolved").textContent).toBe("Resolved");
    expect(tab("All").textContent).toBe("All");
    expect(tab("Open").textContent).toBe("Open 1");
    expect(announced()).not.toContain("0 resolved");
    expect(announced()).toContain(expected);
    fireEvent.click(tab("Open"));
    expect(announced()).not.toContain(expected);
    expect(announced()).toBe("1 open · 0 resolved");
  });

  it("does not call a scope-emptied screen a failed search", () => {
    /*
     * "No incidents match" sends the reader to clear a filter. Under a scope
     * that is simply reporting good news there is nothing to clear, and the
     * instruction sends them hunting for an outage that does not exist.
     */
    render(
      <IncidentsView
        incidents={[]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "8": "cdn" }}
      />,
    );

    fireEvent.click(tab("Open"));

    expect(document.body.textContent).not.toMatch(/no incidents match/i);
  });

  it("offers nothing to narrow when there is nothing at all, and keeps the window", () => {
    /*
     * The all-clear has no list to filter, so no tabs and no filter field;
     * the window stays, because a quiet month is when somebody asks about
     * the last ninety days.
     */
    render(
      <IncidentsView incidents={[]} resolved={[]} now={NOW} monitorCount={3} onHistoryDaysChange={() => {}} />,
    );
    const head = header();
    expect(within(head).queryByRole("group", { name: "Filter by state" })).toBeNull();
    expect(within(head).queryByRole("searchbox")).toBeNull();
    expect(within(head).getByRole("button", { name: "History: 30 days" })).toBeTruthy();
    expect(document.body.textContent).toContain("Nothing is broken right now");
  });

  it.each(["scope", "query"])("keeps a %s that still narrows on screen when the lists empty", (kind) => {
    /*
     * A choice made before the lists emptied goes on narrowing them when
     * incidents come back, so its control stays: otherwise nothing on screen
     * would show it, or take it away.
     */
    const view = render(
      <IncidentsView incidents={[incident()]} resolved={[]} now={NOW} names={{ "7": "api" }} />,
    );
    if (kind === "scope") fireEvent.click(tab("Open"));
    else fireEvent.change(within(header()).getByLabelText("Filter incidents by monitor"), { target: { value: "api" } });
    view.rerender(<IncidentsView incidents={[]} resolved={[]} now={NOW} names={{ "7": "api" }} />);
    expect(within(header()).getByRole("group", { name: "Filter by state" })).toBeTruthy();
    expect(within(header()).getByRole("searchbox", { name: "Filter incidents by monitor" })).toBeTruthy();
  });

  it("does not keep the header for a filter of only spaces", () => {
    /*
     * A query of only spaces narrows nothing (the filter trims it), so it is
     * not a choice the header has to keep on screen once the lists empty.
     */
    const view = render(
      <IncidentsView incidents={[incident()]} resolved={[]} now={NOW} names={{ "7": "api" }} />,
    );
    fireEvent.change(within(header()).getByLabelText("Filter incidents by monitor"), { target: { value: "   " } });
    view.rerender(<IncidentsView incidents={[]} resolved={[]} now={NOW} names={{ "7": "api" }} />);
    expect(within(header()).queryByRole("group", { name: "Filter by state" })).toBeNull();
    expect(within(header()).queryByRole("searchbox")).toBeNull();
    expect(document.body.textContent).toContain("Nothing is broken right now");
  });
});
