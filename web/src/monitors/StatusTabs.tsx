import { LED_STATE } from "./ledState";
import { ListTabs } from "./ListTabs";
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
 * that turns the empty list back into the full one. Drawn by `ListTabs`,
 * which the incidents screen's scope uses too.
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

/** All has no status, and a tab needs a key. */
const ALL = "all";

export function StatusTabs({
  summary,
  status,
  onChange,
}: {
  summary: Summary;
  status: MonitorStatus | null;
  onChange: (next: MonitorStatus | null) => void;
}) {
  const shown = TABS.filter(
    (tab) => tab.status === null || summary[tab.status] > 0 || tab.status === status,
  );
  return (
    <ListTabs
      label="Filter by status"
      value={status ?? ALL}
      tabs={shown.map((tab) => ({
        key: tab.status ?? ALL,
        label: tab.label,
        count: tab.status === null ? summary.total : summary[tab.status],
        // A key to the colour, not a lamp: the word beside it carries the
        // meaning (DESIGN.md §2.3), so it is decorative. All has none.
        state: tab.status === null ? undefined : LED_STATE[tab.status],
      }))}
      // Pressing the selected status again is a way back to All, as pressing
      // the selected chip used to be.
      onPress={(key) => {
        const pressed = key === ALL ? null : (key as MonitorStatus);
        onChange(pressed === status ? null : pressed);
      }}
    />
  );
}
