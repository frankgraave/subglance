import { useCallback, useState } from "react";
import { QueryClientProvider, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { useLiveMonitors } from "../live/useLiveMonitors";
import type { LiveOptions } from "../live/useLiveMonitors";
import { useNow } from "../live/useNow";
import { IncidentsView } from "./IncidentsView";
import {
  HISTORY_DAYS,
  ackIncident,
  fetchOpenIncidents,
  fetchResolvedIncidents,
  openIncidentsQueryKey,
  resolvedIncidentsQueryKey,
} from "./api";
import { detailQueryKey } from "../monitors/detail";

/**
 * The incidents screen's data owner.
 *
 * Two sources, and they are not the same kind of thing. The incidents come
 * from a polled query, because incidents open and close without an SSE frame
 * that says so — the same reason `LiveMonitorDetail` polls its detail query on
 * a minute. The monitor *names* come from the live list the dashboard already
 * holds, so opening this screen costs one request and not a second copy of
 * every monitor.
 *
 * The names matter more than they look: an incident carries `monitor_id` and
 * nothing else, and a screen that says "Monitor 7 is down" has handed the
 * reader a lookup to do at the worst possible moment.
 */

/**
 * How often to re-ask what is broken.
 *
 * Shorter than the detail view's minute. This is the screen somebody leaves
 * open during an outage, and the two facts on it that move — whether anything
 * recovered, and whether a colleague has acked — are exactly the facts that
 * change what you do next. Fifteen seconds is small enough to feel current
 * without turning a self-hosted box into a poll target.
 */
export const INCIDENTS_POLL_MS = 15_000;

export type LiveIncidentsProps = LiveOptions & {
  /** Fetcher seam for tests; defaults to the real endpoint. */
  fetchIncidents?: typeof fetchOpenIncidents;
  /** History fetcher seam for tests. */
  fetchHistory?: typeof fetchResolvedIncidents;
  ack?: typeof ackIncident;
};

export function LiveIncidents({
  fetchIncidents = fetchOpenIncidents,
  fetchHistory = fetchResolvedIncidents,
  ack = ackIncident,
  ...live
}: LiveIncidentsProps) {
  const queryClient = useQueryClient();
  const { monitors, status } = useLiveMonitors(live);
  const now = useNow();

  const incidents = useQuery({
    queryKey: openIncidentsQueryKey,
    queryFn: ({ signal }) => fetchIncidents(signal),
    refetchInterval: INCIDENTS_POLL_MS,
    staleTime: 5_000,
  });

  /*
   * The 30-day history, assembled from the per-monitor endpoint.
   *
   * It waits for the monitor list rather than firing on mount, because the ids
   * are the input — and it polls far more slowly than the open list. History
   * changes when something recovers, which the open list notices within
   * fifteen seconds anyway; re-reading a month of incidents on that cadence
   * would spend one request per monitor every fifteen seconds to redraw a card
   * that almost never changes.
   */
  const monitorIds = monitors.map((monitor) => monitor.id);
  const history = useQuery({
    queryKey: [...resolvedIncidentsQueryKey(HISTORY_DAYS), monitorIds.join(",")],
    queryFn: ({ signal }) =>
      fetchHistory(monitorIds, HISTORY_DAYS, Date.now(), signal),
    enabled: monitorIds.length > 0,
    refetchInterval: 5 * 60_000,
    staleTime: 60_000,
  });

  /*
   * The row currently being acknowledged, held separately from the mutation.
   *
   * `useMutation` knows *that* something is in flight, not *which* row — and
   * with several open incidents on screen, a spinner on all of them would be
   * a lie about four requests that are not happening.
   */
  /*
   * Every ack in flight, not just the most recent one.
   *
   * A single `ackingId` was overwritten the moment a second incident was
   * clicked: incident A's control re-enabled while its request was still
   * running, so a second click sent a duplicate ack for A. During a cluster
   * outage — exactly when several incidents are acked in quick succession —
   * that is the normal way to use this screen, not an edge case.
   */
  const [ackingIds, setAckingIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const markAcking = useCallback((incidentId: string, busy: boolean) => {
    setAckingIds((current) => {
      if (current.has(incidentId) === busy) return current;
      const next = new Set(current);
      if (busy) next.add(incidentId);
      else next.delete(incidentId);
      return next;
    });
  }, []);

  const mutation = useMutation({
    mutationFn: (id: string) => ack(id),
    onSettled: (_data, _error, id) => {
      markAcking(id, false);
      /*
       * Refetch rather than patch the cache.
       *
       * The optimistic version of this would write `acked: true` locally, and
       * it is tempting because the button is meant to feel immediate. It is
       * also the one place in this screen where guessing is unacceptable: the
       * whole claim of the acked state is "a human has seen this", and a row
       * that says so because the browser assumed the request would succeed is
       * the product lying about the one fact this ticket is about.
       *
       * The monitor's own detail query is invalidated too: the same incident
       * is on that screen, and leaving it saying "not acknowledged" after a
       * successful ack is how two screens end up disagreeing.
       */
      void queryClient.invalidateQueries({ queryKey: openIncidentsQueryKey });
      const incident = incidents.data?.find((candidate) => candidate.id === id);
      if (incident !== undefined) {
        void queryClient.invalidateQueries({
          queryKey: detailQueryKey(incident.monitorId),
        });
      }
    },
  });

  const onAck = useCallback(
    (id: string) => {
      markAcking(id, true);
      mutation.mutate(id);
    },
    [markAcking, mutation],
  );

  const names: Record<string, string> = {};
  for (const monitor of monitors) names[monitor.id] = monitor.name;

  return (
    <IncidentsView
      incidents={incidents.data ?? []}
      resolved={history.data?.incidents ?? []}
      historyTruncated={history.data?.truncated ?? false}
      historyDays={HISTORY_DAYS}
      monitorCount={monitors.length}
      names={names}
      now={now}
      loading={incidents.isPending}
      error={incidents.error instanceof Error ? incidents.error : null}
      onAck={onAck}
      ackingIds={ackingIds}
      ackError={mutation.error instanceof Error ? mutation.error : null}
      /*
       * Anything other than a confirmed live stream is stale, exactly as on
       * the detail view. It does not stop the incidents polling — that has its
       * own transport — but it does stop this screen claiming an outage is
       * ongoing *right now* when the thing that would tell us it ended has
       * stopped talking to us (DESIGN.md §6).
       */
      stale={status !== "live"}
    />
  );
}

/** Mounts the incidents screen with its own query client. */
export function LiveIncidentsRoot(
  props: LiveIncidentsProps & { client?: QueryClient },
) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveIncidents {...rest} />
    </QueryClientProvider>
  );
}
