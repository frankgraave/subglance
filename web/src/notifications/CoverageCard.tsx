import { Card } from "../components/Card";
import { StateChip } from "../components/Chip";
import { IconBell } from "../components/icons";
import type { InventoryMonitor } from "../monitors/inventory";
import type { Channel } from "./channels";
import {
  coverageList,
  describeCoverage,
  describeRepeat,
  silentCount,
  unconfirmedCount,
} from "./coverage";
import type { Coverage } from "./coverage";

/**
 * "Who hears about this monitor", answered for every monitor at once
 * (SUB-124).
 *
 * The routing model is a default plus per-monitor overrides, and the notifier
 * also skips a disabled channel. Each rule is simple; the three together are
 * something nobody should have to evaluate in their head per monitor. This
 * card does it and prints the answer, which is what the mockup's coverage
 * preview was for.
 *
 * Silent monitors are listed first and in the open, because they are the
 * finding. The rest sit behind one disclosure whose summary already states
 * the fact ("every other active monitor reaches a channel"): on an instance
 * with eighty monitors, eighty rows confirming that nothing is wrong would
 * push everything else on the page out of reach.
 *
 * Not red. Red means something is failing right now, and a monitor that
 * would alert nobody has not failed yet — it is the same argument as the
 * empty state's "Alerts are going nowhere" (DESIGN.md §2.3). The count in the
 * card's note and the word "nobody" carry it.
 */

export type CoverageCardProps = {
  monitors: readonly InventoryMonitor[] | null;
  channels: readonly Channel[];
  loading?: boolean;
  /** True when the monitor list could not be read. */
  failed?: boolean;
};

export function CoverageCard({
  monitors,
  channels,
  loading = false,
  failed = false,
}: CoverageCardProps) {
  if (!loading && !failed && (monitors === null || monitors.length === 0)) {
    // Nothing is watched, so nothing can go unheard. The monitors page has
    // its own empty state; repeating it here would be a second one.
    return null;
  }
  const list = monitors === null ? [] : coverageList(monitors, channels);
  const silent = list.filter((c) => c.silent && !c.paused);
  const rest = list.filter((c) => !(c.silent && !c.paused));
  const count = silentCount(list);
  const unconfirmed = unconfirmedCount(list);

  return (
    <Card
      className="nt-card"
      icon={<IconBell />}
      /* `Name (N)` (DESIGN.md §8.3), N being every monitor the card frames.
         While loading or failed the bare title, so a 0 is never read as a
         result — the same rule as the Channels card. */
      title={
        loading || failed ? "Who hears what" : `Who hears what (${list.length})`
      }
      headingLevel={2}
      /* A polite live region, so a screen reader hears the finding change
         when the inventory poll moves the count. Only the sentence: the
         monitor list below stays out of it, or every poll would read it all
         out again. */
      note={
        loading || failed ? undefined : (
          <span role="status">
            {count > 0
              ? `${count} active ${count === 1 ? "monitor alerts" : "monitors alert"} nobody.`
              : unconfirmed > 0
                ? /* Not the all-clear: for these the lists have not settled
                     enough to say, and "every monitor is covered" is only
                     worth something when it has been checked. */
                  `No active monitor is known to alert nobody, but ${unconfirmed} could not be checked yet.`
                : "Every active monitor reaches at least one channel."}
          </span>
        )
      }
    >
      {loading || failed ? (
        /* A failed read is not a finding. Saying "nobody hears about
           anything" because one request 500'd is the claim this card exists
           to make only when it is true. */
        <p className="nt-note">
          {loading
            ? "Loading monitors…"
            : "The monitor list could not be loaded, so who hears about each monitor is unknown."}
        </p>
      ) : (
        <>
          {silent.length > 0 && (
            <ul className="nt-cov" aria-label="Monitors that alert nobody">
              {silent.map((c) => (
                <CoverageRow key={c.id} coverage={c} />
              ))}
            </ul>
          )}
          {rest.length > 0 && (
            <details className="nt-legend nt-cov-more">
              <summary className="nt-legend-summary">
                {silent.length === 0
                  ? `Show all ${rest.length} ${rest.length === 1 ? "monitor" : "monitors"} and their channels`
                  : `Show the other ${rest.length} ${rest.length === 1 ? "monitor" : "monitors"}`}
              </summary>
              <ul className="nt-cov" aria-label="Other monitors">
                {rest.map((c) => (
                  <CoverageRow key={c.id} coverage={c} />
                ))}
              </ul>
            </details>
          )}
          {/* One mechanism, stated once (SUB-124 and SUB-81). Repeat alerts
              are a per-monitor setting. Each one is routed when it is sent,
              so it follows the monitor's current channels and their quiet
              hours, not a snapshot of where the first alert went. The page
              offers no second, per-channel
              repeat switch: two places for one idea is how a silence stops
              being explainable. */}
          <p className="nt-note nt-cov-repeats">
            Repeat alerts for an unacknowledged incident go to the monitor's
            current channels and follow those channels' quiet hours.
            Each monitor sets where its repeats start; every gap is four
            times the last, at most a day, and acknowledging the incident stops
            them.
          </p>
        </>
      )}
    </Card>
  );
}

function CoverageRow({ coverage }: { coverage: Coverage }) {
  const text = describeCoverage(coverage);
  const repeat = describeRepeat(coverage);
  return (
    <li
      className="nt-cov-row"
      data-silent={coverage.silent && !coverage.paused}
      data-paused={coverage.paused}
    >
      <span className="nt-cov-name" title={coverage.name}>
        {coverage.name}
      </span>
      <span className="nt-cov-who">
        {coverage.paused ? (
          /* A paused monitor is not checked, so nobody hearing about it is
             what pausing means rather than a misconfiguration. Its route is
             still printed: it is what applies the moment it is resumed. */
          <>
            <StateChip>paused</StateChip> {text}
          </>
        ) : coverage.route === "unknown" ? (
          <StateChip>not loaded</StateChip>
        ) : (
          text
        )}
        {repeat !== null && (
          <span className="nt-cov-repeat"> · {repeat}</span>
        )}
      </span>
    </li>
  );
}
