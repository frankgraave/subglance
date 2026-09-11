import { useMemo, useState } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import { effectiveLayout } from "../shell/preferences";
import { Dashboard } from "./Dashboard";
import { demoMonitors } from "./demo";
import { describeTransitions } from "./model";

/**
 * Workbench harness for the dashboard.
 *
 * This is the only place in the feature that holds state, and it stands in for
 * what SUB-26 will do with SSE and TanStack Query: own the data, own the
 * previous snapshot, and hand both the list and the announcement down as
 * props. The components below it stay pure, so swapping this harness for a
 * real query hook does not touch them.
 */

const SCALES = [5, 200] as const;

/**
 * Layout choices offered by the harness.
 *
 * The point of the workbench is judging the phone layout on a desktop screen
 * without resizing the window, so the choice is explicit rather than "auto".
 * The narrow-viewport veto still applies below 640px — the harness cannot
 * make a row layout fit where it does not. The status wall is not offered: it
 * deliberately replaces the whole page, so it cannot render inside a harness.
 */
const LAYOUTS = [
  { id: "rows", label: "Rows" },
  { id: "cards", label: "Cards" },
  { id: "compact", label: "Compact" },
] as const;

type Layout = (typeof LAYOUTS)[number]["id"];

export function DashboardWorkbench() {
  const [size, setSize] = useState<(typeof SCALES)[number]>(5);
  const [layout, setLayout] = useState<Layout>("rows");
  const [query, setQuery] = useState("");

  // The buttons report what was asked for; this reports what `Dashboard`
  // actually renders. Below 640px the two differ, and a harness built for
  // judging layouts must not claim to be showing one it is not.
  const narrow = useCompactViewport();
  const shown = effectiveLayout(layout, narrow);

  const monitors = useMemo(() => demoMonitors(size), [size]);

  // Demonstrates the live region wiring without inventing an update loop:
  // the sentence that *would* be announced if the previous render had been an
  // all-healthy list. With no data source there are no real transitions yet.
  const announcement = useMemo(
    () => describeTransitions(monitors.map((m) => ({ ...m, status: "up" as const })), monitors),
    [monitors],
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2 text-helper text-ink-3">
        <span>Scale:</span>
        {SCALES.map((n) => (
          <button
            key={n}
            type="button"
            onClick={() => setSize(n)}
            aria-pressed={size === n}
            className={`rounded-sm border px-2 py-1 text-helper transition-colors ${
              size === n
                ? "border-border-hi bg-surface-2 text-ink"
                : "border-border text-ink-3 hover:text-ink-2"
            }`}
          >
            {n} monitors
          </button>
        ))}
        <span className="text-ink-4">
          Deterministic fixtures — no virtualisation, all {size} rows are in the DOM.
        </span>
      </div>

      <div className="flex items-center gap-2 text-helper text-ink-3">
        <span>Layout:</span>
        {LAYOUTS.map((option) => (
          <button
            key={option.id}
            type="button"
            onClick={() => setLayout(option.id)}
            aria-pressed={layout === option.id}
            className={`rounded-sm border px-2 py-1 text-helper transition-colors ${
              layout === option.id
                ? "border-border-hi bg-surface-2 text-ink"
                : "border-border text-ink-3 hover:text-ink-2"
            }`}
          >
            {option.label}
          </button>
        ))}
        <span className="text-ink-4">
          {shown === layout
            ? "Forced, so the phone layout can be judged on a desktop."
            : `Viewport veto below 640px — rendering ${shown}.`}
        </span>
      </div>

      <Dashboard
        monitors={monitors}
        query={query}
        onQueryChange={setQuery}
        announcement={announcement}
        layout={layout}
      />
    </div>
  );
}
