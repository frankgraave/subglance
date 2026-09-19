import { useCallback, useState } from "react";
import {
  QueryClientProvider,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
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
 * Three sources, and they are not the same kind of thing. The open incidents
 * come from a polled query, because incidents open and close without an SSE
 * frame that says so — the same reason `LiveMonitorDetail` polls its detail
 * query on a minute. The resolved history is a second, slower query against
 * its own endpoint, paged by cursor. The monitor *names* come from the live
 * list the dashboard already holds, so opening this screen costs two requests
 * and not a second copy of every monitor.
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
  const {
    monitors,
    status,
    loading: monitorsLoading,
    error: monitorsError,
  } = useLiveMonitors(live);
  const now = useNow();

  const incidents = useQuery({
    queryKey: openIncidentsQueryKey,
    queryFn: ({ signal }) => fetchIncidents(signal),
    refetchInterval: INCIDENTS_POLL_MS,
    staleTime: 5_000,
  });

  /*
   * The history, in one paged request rather than one per monitor.
   *
   * `GET /api/v1/incidents/resolved` answers the instance-wide question
   * directly, so this no longer waits for the monitor list, no longer sends a
   * request per monitor, and no longer stops at 24 of them. It still polls far
   * more slowly than the open list: history changes when something recovers,
   * which the open list notices within fifteen seconds anyway.
   *
   * `useInfiniteQuery` rather than a page of state, because the cursor is the
   * API's and the pages have to stay in order across a refetch — React Query
   * re-walks the cursors it already has, which is precisely what a hand-rolled
   * "append to an array" would get wrong the first time the window changed.
   */
  const [historyDays, setHistoryDays] = useState(HISTORY_DAYS);
  const history = useInfiniteQuery({
    queryKey: resolvedIncidentsQueryKey(historyDays),
    queryFn: ({ pageParam, signal }) =>
      fetchHistory(historyDays, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => (last.hasMore ? last.nextCursor : undefined),
    refetchInterval: 5 * 60_000,
    staleTime: 60_000,
  });

  /*
   * Every page loaded so far, flattened.
   *
   * The order is the API's — newest resolution first, and each page continues
   * where the previous ended — so concatenating pages preserves it without a
   * re-sort here. Sorting again would be a second opinion about an order the
   * cursor already guarantees, and the two could disagree when two incidents
   * resolve in the same second.
   */
  const resolved = (history.data?.pages ?? []).flatMap((page) => page.incidents);

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
      resolved={resolved}
      historyDays={historyDays}
      onHistoryDaysChange={setHistoryDays}
      historyHasMore={history.hasNextPage}
      onLoadMoreHistory={() => void history.fetchNextPage()}
      historyLoadingMore={history.isFetchingNextPage}
      historyError={history.error instanceof Error ? history.error : null}
      historyLoading={history.isPending}
      /*
       * The count is only passed once the monitor list is actually known.
       *
       * `useLiveMonitors` reports an empty array while loading and after a
       * failed fetch, so passing `monitors.length` unconditionally let the
       * empty state announce "0 monitors watched, zero confirmed outages" —
       * an authoritative all-clear about a population we had failed to read.
       * Undefined makes the view fall back to the wording that claims nothing.
       */
      monitorCount={
        monitorsLoading || monitorsError !== null ? undefined : monitors.length
      }
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
