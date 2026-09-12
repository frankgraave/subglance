import type { ConnectionStatus } from "./connection";

/**
 * The "you are looking at frozen numbers" indicator.
 *
 * Design reasoning, because this is the one piece of chrome that exists to
 * contradict the rest of the screen:
 *
 * 1. **It is only visible when it has something to say.** A green "live" badge
 *    on a healthy dashboard is decoration that trains people to ignore the
 *    element, which is precisely the element that must not be ignored when it
 *    turns. So `live` renders nothing at all, and the first connection attempt
 *    renders nothing either — a warning during the initial 200ms would be
 *    alarmism about a page that is merely loading.
 * 2. **Text, not a coloured dot.** "Reconnecting…" says what is happening;
 *    an amber dot needs a legend. It also survives the ~8% of men who cannot
 *    separate red from green (DESIGN.md §2.3).
 * 3. **`role="status"`, not `role="alert"`.** Polite: it is read after the
 *    current utterance rather than interrupting it, which matters because the
 *    dashboard already owns a live region for outages and an assertive badge
 *    would talk over the announcement that someone actually needs.
 * 4. **`--warn`, never `--down`.** A lost connection is not an outage. Using
 *    the outage colour would make the dashboard look like everything failed at
 *    the moment it lost the ability to know anything at all.
 * 5. **The button is always offered, never a spinner.** Reconnecting runs on a
 *    backoff ladder that can be sitting on a thirty-second wait, and somebody
 *    staring at a dashboard they already know is broken must not have to sit
 *    out our patience (DESIGN.md §6). Disabling it while an attempt is in
 *    flight would recreate exactly that wait.
 */

import { describeAge } from "./age";

export type ConnectionBadgeProps = {
  status: ConnectionStatus;
  /** When the newest data on screen was fetched; shown so staleness is dateable. */
  since?: number | null;
  /**
   * Passed in rather than read here. `Date.now()` during render is impure, and
   * the owner already holds a clock it can share (see useNow).
   */
  now: number;
  /** Reopens the stream immediately. Omitted, the button is not rendered. */
  onReconnect?: () => void;
};

export function ConnectionBadge({ status, since = null, now, onReconnect }: ConnectionBadgeProps) {
  // Nothing to say: a working connection is the assumption, so it is silent.
  if (status !== "offline") {
    return <div role="status" aria-live="polite" className="sr-only" />;
  }

  const age = describeAge(since, now);

  return (
    <div role="status" aria-live="polite" className="conn-badge" data-state="offline">
      <span className="conn-badge-dot" aria-hidden="true" />
      <span>
        Connection lost — reconnecting
        {age !== null && <span className="conn-badge-age"> · updated {age}</span>}
      </span>
      {onReconnect !== undefined && (
        <button type="button" className="conn-badge-retry" onClick={onReconnect}>
          Reconnect now
        </button>
      )}
    </div>
  );
}
