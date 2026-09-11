import { partition, summarise } from "../monitors/model";
import { Led } from "../monitors/Led";
import type { Monitor } from "../monitors/types";
import { formatClock, useSecondsClock } from "./clock";

/**
 * The wall display: lamp and name, nothing else (DESIGN.md §7).
 *
 * This layout exists to be looked at from across a room, and everything in it
 * follows from that.
 *
 * **No sidebar and no topbar.** Hiding the chrome is the whole reason the
 * layout exists; that is done by the shell, which renders this component
 * instead of the shell frame rather than inside it.
 *
 * **Broken cards take a warm border, never a coloured fill.** A wall of
 * coloured tiles is noise — every card shouting means no card is heard. A
 * quiet wall with two warm edges is information (DESIGN.md §6, §10). The same
 * rule the table row already follows.
 *
 * **The clock is not decoration.** A wall display that has not changed in an
 * hour is indistinguishable from a browser that died an hour ago. A ticking
 * second is the cheapest possible proof of life, and it is the only moving
 * thing here on purpose.
 *
 * **The header whispers.** Instance name, a count, the time — in `--ink-3` and
 * `--ink-4`, because if the room can read the header at a glance it is
 * competing with the lamps, which are the actual signal.
 */

export type StatusWallProps = {
  monitors: readonly Monitor[];
  /** The instance name in the header line. */
  instance?: string;
  /** True when the stream is down: the canvas itself carries the warning. */
  stale?: boolean;
  /** Leaves the wall. Wired to Esc by the shell; also a visible control. */
  onExit?: () => void;
  /**
   * Replaces the count and the empty state when there is no list to describe
   * yet — a first load in flight, or one that failed. The wall still renders
   * its header, clock and exit, because a blank screen with no way out is the
   * worst thing a wall display can become.
   */
  notice?: string;
  /** Injected in tests, which must not depend on the machine's clock. */
  now?: number;
};

export function StatusWall({
  monitors,
  instance,
  stale = false,
  onExit,
  now,
  notice,
}: StatusWallProps) {
  // The hook cannot be skipped, so the clock always runs and the override
  // wins afterwards — the same shape `Dashboard` uses for its media query.
  const tick = useSecondsClock();
  const clock = formatClock(now ?? tick);

  const summary = summarise(monitors);
  // Down first, then alphabetical: the same `partition` the row and card
  // layouts call, so "needs attention" means one thing across the product.
  const { attention, rest } = partition(monitors);
  const ordered = [...attention, ...rest];

  return (
    <main className="wall" data-stale={stale ? "true" : "false"}>
      <div className="wall-stage">
        <header className="wall-head">
          <h1 className="wall-title">{instance !== undefined && instance !== "" ? instance : "SubGlance"}</h1>
          <p className="wall-meta">
            {notice !== undefined ? (
              notice
            ) : (
              <>
                {summary.total} {summary.total === 1 ? "monitor" : "monitors"}
                {summary.down > 0 && (
                  <>
                    {" · "}
                    {/*
                     * While the stream is stale the count is history, not news,
                     * and it is labelled as such. A wall that keeps announcing
                     * "1 down" in the present tense after it stopped hearing
                     * anything is exactly the confident lie §6 forbids — the
                     * number may have been fixed, or nine more may have joined
                     * it.
                     */}
                    {stale ? (
                      <span className="wall-meta-lastknown">{summary.down} down, last known</span>
                    ) : (
                      <b className="wall-meta-down">{summary.down} down</b>
                    )}
                  </>
                )}
              </>
            )}
            {/*
             * The stale suffix, not a banner. There is no chrome to put a
             * banner in, and growing one here would defeat the layout — so
             * the warning rides the line that is already there, next to a
             * warm border around the viewport.
             */}
            {stale && <span className="wall-meta-stale"> · connection lost, not updating</span>}
          </p>
          <p className="wall-clock" aria-hidden="true">
            {/*
             * Hidden from assistive technology: a value that changes every
             * second would make a screen reader recite the time forever, and
             * the proof-of-life it offers is purely visual anyway.
             */}
            {clock}
          </p>
        </header>

        {ordered.length === 0 ? (
          <p className="wall-empty">{notice ?? "Nothing being watched yet."}</p>
        ) : (
          <ul className="wall-grid">
            {ordered.map((monitor) => (
              <li key={monitor.id} className="wall-card" data-status={monitor.status}>
                <Led status={monitor.status} className="wall-card-led" />
                <span className="wall-card-name">{monitor.name}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {onExit !== undefined && (
        // Visible, not Esc-only. A wall display is often a machine nobody is
        // sitting at; a keyboard-only exit strands whoever walks up to it.
        <button type="button" className="wall-exit shell-icon-btn" onClick={onExit} title="Leave the status wall (Esc)">
          <span className="sr-only">Leave the status wall</span>
          <svg viewBox="0 0 24 24" className="shell-icon" aria-hidden="true" focusable="false">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      )}
    </main>
  );
}
