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
export function EmptyState({ query, totalCount }: { query: string; totalCount: number }) {
  const searching = query.trim() !== "" && totalCount > 0;
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
      {searching ? (
        <>
          <h3 className="mon-empty-title">No monitors match “{query.trim()}”</h3>
          <p className="mon-empty-body">
            Search looks at monitor names and targets. Check the spelling, or clear the search to
            see all {totalCount} monitors.
          </p>
        </>
      ) : (
        <>
          <h3 className="mon-empty-title">No monitors yet</h3>
          <p className="mon-empty-body">
            Add the first thing you want watched — a URL, a host and port, or a cron job that
            should check in. SubGlance starts probing it straight away and this page fills in as
            the first results land.
          </p>
        </>
      )}
    </div>
  );
}
