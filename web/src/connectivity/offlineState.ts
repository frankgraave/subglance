import { useQuery } from "@tanstack/react-query";
import { useNow } from "../live/useNow";
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

/**
 * When the server's own connection went, or null when it has not (or when
 * that is not known right now).
 *
 * Null on every doubtful path — the check switched off, the endpoint missing
 * or failing, an answer older than TRUST_MS — because this drives a sentence
 * that explains away warnings. Wrongly telling someone their monitors are
 * fine and the server is the problem is the dangerous direction of the two.
 */
export function useHostOfflineSince(): number | null {
  const query = useQuery({
    queryKey: connectivityKey,
    queryFn: ({ signal }) => fetchConnectivity(signal),
    refetchInterval: POLL_MS,
  });
  const now = useNow();
  const data = query.data;
  if (data === undefined || query.isError || now - query.dataUpdatedAt > TRUST_MS) return null;
  return data.offline ? data.offlineSince : null;
}

/**
 * "03:12" today, "28 Sep, 23:40" on an earlier day.
 *
 * The day is only added when it differs: "since 23:40" read the next morning
 * silently loses a night, which is exactly the length of outage someone would
 * most need to know about.
 */
export function formatSince(since: number, now: number): string {
  const at = new Date(since);
  const today = new Date(now);
  const time = at.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  const sameDay =
    at.getFullYear() === today.getFullYear() &&
    at.getMonth() === today.getMonth() &&
    at.getDate() === today.getDate();
  if (sameDay) return time;
  return `${at.toLocaleDateString(undefined, { day: "numeric", month: "short" })}, ${time}`;
}

/** The sentence, shared by the dashboard banner and the status wall. */
export function hostOfflineText(since: number, now: number): string {
  return `No outbound connection since ${formatSince(since, now)}`;
}
