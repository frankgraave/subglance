import { useQuery } from "@tanstack/react-query";
import { useNow } from "../live/useNow";
import { formatClock, formatDayTime, sameDay } from "../format/format";
import { connectivityKey, fetchConnectivity } from "./api";

/*
 * The hook and the wording behind HostOfflineNotice. Their own module because
 * a .tsx that exports helpers as well as components breaks fast refresh.
 */

/**
 * How often the dashboard asks. Reading the endpoint never dials anything —
 * it reports what the server's last canary round found — so this costs one
 * cheap local request, and the canary itself re-dials every 30 seconds while
 * the server is offline. Fifteen seconds means the line appears and clears
 * within one canary period of the change.
 */
const POLL_MS = 15_000;

/**
 * Past this without a fresh answer, the last one is no longer vouched for.
 * Three polls: long enough to ride out one slow request, short enough that a
 * stale "no outbound connection" does not linger after the server is back.
 */
const TRUST_MS = 45_000;

/** What the server has lost, and since when (epoch milliseconds). */
export interface HostProblem {
  /** `offline`: no outbound connection. `resolver`: no answer from its DNS resolver. */
  kind: "offline" | "resolver";
  since: number;
}

/**
 * What the server's own connection has lost and since when, or null when
 * nothing (or when that is not known right now).
 *
 * Null on every doubtful path — the check switched off, the endpoint missing
 * or failing, an answer older than TRUST_MS — because this drives a sentence
 * that explains away warnings. Wrongly telling someone their monitors are
 * fine and the server is the problem is the dangerous direction of the two.
 */
export function useHostProblem(): HostProblem | null {
  const query = useQuery({
    queryKey: connectivityKey,
    queryFn: ({ signal }) => fetchConnectivity(signal),
    refetchInterval: POLL_MS,
  });
  const now = useNow();
  const data = query.data;
  if (data === undefined || query.isError || now - query.dataUpdatedAt > TRUST_MS) return null;
  if (data.offline && data.offlineSince !== null) return { kind: "offline", since: data.offlineSince };
  if (data.resolverDown && data.resolverDownSince !== null) {
    return { kind: "resolver", since: data.resolverDownSince };
  }
  return null;
}

/**
 * "03:12" today, "28 Sep, 23:40" on an earlier day.
 *
 * The day is only added when it differs: "since 23:40" read the next morning
 * silently loses a night, which is exactly the length of outage someone would
 * most need to know about.
 */
export function formatSince(since: number, now: number): string {
  return sameDay(since, now) ? formatClock(since) : formatDayTime(since);
}

/**
 * The sentence, shared by the dashboard banner and the status wall.
 *
 * The resolver's names SubGlance rather than "this host", for the reason
 * `causeWords` gives for `local_network`: the reader is at a browser on some
 * other machine, and "this host" would point at theirs.
 */
export function hostProblemText(problem: HostProblem, now: number): string {
  const since = formatSince(problem.since, now);
  return problem.kind === "offline"
    ? `No outbound connection since ${since}`
    : `SubGlance's DNS resolver has not answered since ${since}`;
}

/** What the held warnings are, worded for the kind of problem. */
export function hostProblemHeld(problem: HostProblem): string {
  return problem.kind === "offline"
    ? "failed network checks are held as warnings, not outages"
    : "failed lookups are held as warnings, not outages";
}
