import { LED_STATE } from "./ledState";
import type { Summary } from "./model";
import type { MonitorStatus } from "./types";

/**
 * The statuses as tabs with their counts, at the head of the dashboard's list
 * (SUB-183): All 14 · Down 3 · Warning 1 · Paused 1 · Up 9.
 *
 * They replace the status chips that sat in the page toolbar, and the list's
 * own “Monitors (N)” title: All carries the total. The behaviour is the
 * chips', with an explicit All. A tab whose count drops to zero leaves the
 * row unless it is the one selected, because it is then the only control
 * that turns the empty list back into the full one.
 *
 * Buttons with `aria-pressed` in a labelled group, not `role="tab"`: a tab
 * promises a panel of its own, and this narrows one list in place. It is the
 * same choice `SegmentedControl` makes for the same reason, and arrow-key
 * semantics would fight the controls beside it.
 */
const TABS: readonly { status: MonitorStatus | null; label: string }[] = [
  { status: null, label: "All" },
  { status: "down", label: "Down" },
  { status: "recovering", label: "Recovering" },
  { status: "warning", label: "Warning" },
  { status: "pending", label: "Pending" },
  { status: "waiting", label: "Waiting" },
  { status: "paused", label: "Paused" },
  { status: "up", label: "Up" },
];

export function StatusTabs({
  summary,
  status,
  onChange,
}: {
  summary: Summary;
  status: MonitorStatus | null;
  onChange: (next: MonitorStatus | null) => void;
}) {
  return (
    <div className="mon-tabs" role="group" aria-label="Filter by status">
      {TABS.filter(
        (tab) =>
          tab.status === null ||
          summary[tab.status] > 0 ||
          tab.status === status,
      ).map((tab) => (
        <button
          key={tab.label}
          type="button"
          className="mon-tab"
          aria-pressed={status === tab.status}
          // Pressing the selected status again is a way back to All, as
          // pressing the selected chip used to be.
          onClick={() =>
            onChange(tab.status === status ? null : tab.status)
          }
        >
          {/* A key to the colour, not a lamp: the word beside it carries the
              meaning (DESIGN.md §2.3), so it is decorative. All has none. */}
          {tab.status === null ? null : (
            <span
              className="mon-count-dot"
              data-state={LED_STATE[tab.status]}
              aria-hidden="true"
            />
          )}
          {tab.label}{" "}
          <span className="mon-tab-count">
            {tab.status === null ? summary.total : summary[tab.status]}
          </span>
        </button>
      ))}
    </div>
  );
}
