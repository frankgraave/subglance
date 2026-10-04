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
  onAddMonitor,
  onClearFilters,
  headingLevel = 2,
}: {
  query: string;
  totalCount: number;
  /** True when a status or tag filter is on, even with an empty query. */
  filtered?: boolean;
  /**
   * One level under whatever heads the list it replaces: 3 on the dashboard
   * and in the inventory, where it sits inside an `h2` card. 2 is the default
   * for a caller that draws it straight under the page's `h1`.
   */
  headingLevel?: 2 | 3;
  /**
   * Opens the add-monitor form, when the reader may add one.
   *
   * The dashboard and the inventory both pass it for an editor or an admin:
   * the form is the route `/monitors/new`, so the button is a real way in from
   * either screen. Absent means the reader cannot add a monitor (a viewer), or
   * the caller has no router (the workbench) — then the step is stated as who
   * can take it and where, rather than as a control this reader does not have.
   */
  onAddMonitor?: () => void;
  /**
   * Clears every filter at once, offered under “no monitors match”.
   *
   * The way back from an empty result is a control rather than directions to
   * several: the tab, the tag panel and the text field each hold part of the
   * narrowing, and naming all three is a sentence nobody acts on (SUB-183).
   */
  onClearFilters?: () => void;
}) {
  const needle = query.trim();
  // A filter that hides every monitor used to render "No monitors yet", which
  // tells a self-hoster their install is empty when in fact they pressed a
  // chip. Any active narrowing counts, not just a typed query.
  const narrowed = (needle !== "" || filtered) && totalCount > 0;
  // One level under what heads it, never a skipped level (SUB-167, SUB-182).
  const Heading = `h${headingLevel}` as "h2" | "h3";
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
          <Heading className="mon-empty-title">
            {needle === ""
              ? "No monitors match these filters"
              : `No monitors match \u201C${needle}\u201D`}
          </Heading>
          <p className="mon-empty-body">
            {needle === ""
              ? `Clear the status tab and the tag filters to see all ${totalCount} monitors.`
              : filtered
                ? `Search looks at monitor names and targets. Check the spelling, or clear the search and the active filters to see all ${totalCount} monitors.`
                : `Search looks at monitor names and targets. Check the spelling, or clear the search to see all ${totalCount} monitors.`}
          </p>
          {onClearFilters === undefined ? null : (
            <p className="mon-empty-action">
              <button
                type="button"
                className="button"
                onClick={onClearFilters}
              >
                Clear all filters
              </button>
            </p>
          )}
        </>
      ) : (
        <>
          <Heading className="mon-empty-title mon-empty-title--first">
            Nothing is being watched yet
          </Heading>
          <p className="mon-empty-body mon-empty-body--first">
            Point SubGlance at the first thing you care about — a URL, a host
            and port, or a cron job that should check in. Probing starts
            immediately, and this page fills in as the first results land.
          </p>
          {/*
           * The one next step, stated as one thing to press.
           *
           * §14 allows this screen to raise its voice, and it works only
           * because everything around it is quiet: there is no list competing
           * for attention here, so a single warm heading and a single obvious
           * action are legible rather than loud. A second button would undo
           * that — "clear next step" means one step.
           */}
          <p className="mon-empty-action">
            {onAddMonitor === undefined ? (
              /* Names the page and the roles instead of a button: a viewer
                 has no add control anywhere, and "press Add monitor" pointed
                 them at one that is not on their screen. */
              <span className="mon-empty-hint">
                An editor or an admin adds monitors on the{" "}
                <b className="mon-empty-control">Monitors</b> page.
              </span>
            ) : (
              <button
                type="button"
                className="mon-empty-cta"
                onClick={onAddMonitor}
              >
                Add the first monitor
              </button>
            )}
          </p>
        </>
      )}
    </div>
  );
}
