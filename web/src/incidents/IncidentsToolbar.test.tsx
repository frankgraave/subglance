// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IncidentsView } from "./IncidentsView";
import { setToolbarSlot } from "../shell/topbarSlot";
import type { Incident } from "../monitors/detail";

/**
 * SUB-136, the incidents half: the screen fills its own toolbar slot.
 *
 * The slot has existed since SUB-131 and stayed empty on purpose — the scope
 * filter it named had nothing behind it, and the ticket is explicit that a
 * disabled control promising a function it does not have is worse than an
 * empty bar. So the rule under test is not "there are two selects". It is the
 * stronger one the ticket actually states: **every control in this bar does
 * what it says, and a control whose wiring is absent is not rendered at all.**
 *
 * The toolbar is a portal target that the shell owns, so these tests supply a
 * stand-in rather than mounting the whole shell: what is under test is the
 * incidents screen's contribution, and dragging the theme toggle in would let
 * an unrelated change fail this file.
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

let toolbar: HTMLElement;

function renderWithToolbar(ui: Parameters<typeof render>[0]) {
  toolbar = document.createElement("div");
  document.body.append(toolbar);
  setToolbarSlot(toolbar);
  return render(ui);
}

afterEach(() => {
  cleanup();
  // Otherwise the next test portals into this test's detached node and its
  // controls land somewhere nothing can query.
  setToolbarSlot(null);
});

describe("the incidents toolbar slot is filled, and everything in it works", () => {
  it("contributes its controls to the toolbar, not to the masthead", () => {
    renderWithToolbar(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        onHistoryDaysChange={() => {}}
      />,
    );
    expect(within(toolbar).getByLabelText("Show")).toBeTruthy();
    expect(within(toolbar).getByLabelText("History")).toBeTruthy();
  });

  it("scopes to open incidents by hiding the history card, not emptying it", () => {
    /*
     * An empty Resolved card under a scope that excludes it would read as
     * "nothing resolved" — a claim about the instance rather than about the
     * filter, and exactly the kind of accidental good news this screen may
     * never print.
     */
    renderWithToolbar(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "7": "api", "8": "cdn" }}
      />,
    );
    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "open" },
    });

    expect(screen.getByRole("heading", { name: "Open incidents" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Resolved" })).toBeNull();
    expect(document.body.textContent).not.toMatch(/nothing resolved/i);
  });

  it("scopes to resolved by hiding the open card", () => {
    renderWithToolbar(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "7": "api", "8": "cdn" }}
      />,
    );

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "resolved" },
    });

    expect(screen.queryByRole("heading", { name: "Open incidents" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();
  });

  it("keeps the Resolved card present when the scope asked for it and it is empty", () => {
    /*
     * Asking for resolved and getting no card back is indistinguishable from
     * the screen having lost the control. The card stays and states its own
     * emptiness, which is the same rule the undated-incident case follows.
     */
    renderWithToolbar(
      <IncidentsView incidents={[incident()]} resolved={[]} now={NOW} />,
    );

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "resolved" },
    });

    expect(screen.getByRole("heading", { name: "Resolved" })).toBeTruthy();
    expect(document.body.textContent).toMatch(/nothing resolved in the last/i);
  });

  it("moves the history window through the owner rather than locally", () => {
    /*
     * The window is a refetch, so the control reports upward instead of
     * filtering what is already on screen. Changing it locally would show a
     * "90 days" label over 30 days of data.
     */
    const onHistoryDaysChange = vi.fn();
    renderWithToolbar(
      <IncidentsView
        incidents={[]}
        resolved={[resolvedIncident]}
        now={NOW}
        historyDays={30}
        onHistoryDaysChange={onHistoryDaysChange}
      />,
    );

    fireEvent.change(within(toolbar).getByLabelText("History"), {
      target: { value: "90" },
    });

    expect(onHistoryDaysChange).toHaveBeenCalledWith(90);
  });

  it("omits the window control when nothing is listening to it", () => {
    /*
     * The ticket's rule, stated as a test: a select that silently does nothing
     * is worse than an absent one, so a caller with no refetch gets no
     * control. Scope survives, because scope needs nobody's cooperation.
     */
    renderWithToolbar(
      <IncidentsView incidents={[incident()]} resolved={[]} now={NOW} />,
    );
    expect(within(toolbar).queryByLabelText("History")).toBeNull();
    expect(within(toolbar).getByLabelText("Show")).toBeTruthy();
  });

  it("counts what is on screen, and the count follows the scope", () => {
    renderWithToolbar(
      <IncidentsView
        incidents={[incident()]}
        resolved={[resolvedIncident]}
        now={NOW}
      />,
    );
    expect(toolbar.textContent).toContain("1 open · 1 resolved");

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "open" },
    });
    expect(toolbar.textContent).toContain("1 open · 0 resolved");
  });

  it("does not call a scope-emptied screen a failed search", () => {
    /*
     * "No incidents match" sends the reader to clear a filter. Under a scope
     * that is simply reporting good news there is nothing to clear, and the
     * instruction sends them hunting for an outage that does not exist.
     */
    renderWithToolbar(
      <IncidentsView
        incidents={[]}
        resolved={[resolvedIncident]}
        now={NOW}
        names={{ "8": "cdn" }}
      />,
    );

    fireEvent.change(within(toolbar).getByLabelText("Show"), {
      target: { value: "open" },
    });

    expect(document.body.textContent).not.toMatch(/no incidents match/i);
  });
});
