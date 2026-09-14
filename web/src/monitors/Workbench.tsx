import { useMemo, useState } from "react";
import { SegmentedControl } from "../components/SegmentedControl";
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
 * The scale choice changes *which fixtures* are on screen, not how they are
 * drawn, so it is the `data` variant of the segmented control (DESIGN.md §7.8).
 * The layout choice below it is the `view` variant. Having both variants in
 * one harness is deliberate: the difference is only judgeable side by side.
 */
const SCALE_OPTIONS = SCALES.map((n) => ({
  id: String(n) as `${(typeof SCALES)[number]}`,
  label: `${n} monitors`,
}));

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
    () =>
      describeTransitions(
        monitors.map((m) => ({ ...m, status: "up" as const })),
        monitors,
      ),
    [monitors],
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2 text-helper text-ink-3">
        <span>Scale:</span>
        <SegmentedControl
          label="Fixture scale"
          variant="data"
          options={SCALE_OPTIONS}
          value={String(size) as (typeof SCALE_OPTIONS)[number]["id"]}
          onChange={(next) => setSize(Number(next) as (typeof SCALES)[number])}
        />
        <span className="text-ink-4">
          Deterministic fixtures — no virtualisation, all {size} rows are in the
          DOM.
        </span>
      </div>

      <div className="flex items-center gap-2 text-helper text-ink-3">
        <span>Layout:</span>
        <SegmentedControl
          label="Harness layout"
          options={LAYOUTS}
          value={layout}
          onChange={setLayout}
        />
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
