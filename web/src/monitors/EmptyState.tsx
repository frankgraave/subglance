import { Led } from "./Led";

/**
 * The empty dashboard is the onboarding (DESIGN.md §7.6): it is the first
 * thing a new self-hoster sees, so it says what to do next rather than
 * shrugging. The no-results case is a different message on purpose — "nothing
 * matched" and "nothing exists" call for different next actions.
 *
 * Shared by the desktop table and the phone card list: the words a beginner
 * reads first should not depend on which device they opened.
 */
export function EmptyState({
  query,
  totalCount,
  filtered = false,
}: {
  query: string;
  totalCount: number;
  /** True when a status or tag filter is on, even with an empty query. */
  filtered?: boolean;
}) {
  const needle = query.trim();
  // A filter that hides every monitor used to render "No monitors yet", which
  // tells a self-hoster their install is empty when in fact they pressed a
  // chip. Any active narrowing counts, not just a typed query.
  const narrowed = (needle !== "" || filtered) && totalCount > 0;
  return (
    <div className="mon-empty">
      {/* Three empty sockets, not three grey lamps. Decorative and
          aria-hidden: nothing is being watched at all here, so neither "no
          reading yet" nor "switched off" applies, and an unfilled lens is the
          honest drawing of a dashboard with nothing plugged into it. */}
      <div className="mon-empty-leds" aria-hidden="true">
        <Led status="paused" labelled={false} />
        <Led status="paused" labelled={false} />
        <Led status="paused" labelled={false} />
      </div>
      {narrowed ? (
        <>
          <h3 className="mon-empty-title">
            {needle === ""
              ? "No monitors match this filter"
              : `No monitors match \u201C${needle}\u201D`}
          </h3>
          <p className="mon-empty-body">
            {needle === ""
              ? `Clear the status or tag filter to see all ${totalCount} monitors.`
              : filtered
                ? `Search looks at monitor names and targets. Check the spelling, or clear the search and the active filters to see all ${totalCount} monitors.`
                : `Search looks at monitor names and targets. Check the spelling, or clear the search to see all ${totalCount} monitors.`}
          </p>
        </>
      ) : (
        <>
          <h3 className="mon-empty-title">No monitors yet</h3>
          <p className="mon-empty-body">
            Add the first thing you want watched — a URL, a host and port, or a
            cron job that should check in. SubGlance starts probing it straight
            away and this page fills in as the first results land.
          </p>
        </>
      )}
    </div>
  );
}
