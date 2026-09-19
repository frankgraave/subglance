/**
 * The dashboard's data owner: one fetch, then a stream.
 *
 * TanStack Query holds the list and the SSE stream writes into that same
 * cache with `setQueryData`. Two stores would mean two versions of the truth
 * and a race every time a refetch and a heartbeat land together; the query
 * cache being the only home for the list removes that by construction.
 *
 * Everything below the hook stays presentational: it hands down a list, a
 * connection status and an announcement, exactly the props Dashboard already
 * takes.
 */

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LiveConnection } from "./connection";
import type { ConnectionStatus, EventSourceFactory } from "./connection";
import { parseEvent } from "./events";
import { applyHeartbeat, applyStatus } from "./apply";
import { fetchMonitors, monitorsQueryKey } from "./api";
import { describeTransitions } from "../monitors/model";
import type { Monitor } from "../monitors/types";

export type UseLiveMonitors = {
  monitors: Monitor[];
  status: ConnectionStatus;
  /** Reopens the stream now, skipping the backoff ladder. */
  reconnect: () => void;
  /** True until the first fetch resolves. */
  loading: boolean;
  /** The fetch error, if the list could not be loaded at all. */
  error: Error | null;
  /** Sentence for the dashboard's live region, or null for silence. */
  announcement: string | null;
};

export type LiveOptions = {
  /** Injected in tests; defaults to the browser's EventSource. */
  createEventSource?: EventSourceFactory;
  streamUrl?: string;
};

const EMPTY: Monitor[] = [];

/**
 * How long to wait before a second resync caused by an unknown monitor id.
 *
 * Long enough that the burst of frames following one creation costs a single
 * refetch, short enough that a second creation moments later is still visible
 * well inside the check interval.
 */
export const RESYNC_THROTTLE_MS = 2_000;

export function useLiveMonitors(options: LiveOptions = {}): UseLiveMonitors {
  const queryClient = useQueryClient();
  // Survives re-renders without causing one; nothing on screen depends on it.
  const lastResyncRef = useRef(0);
  const { streamUrl = "/api/v1/stream", createEventSource } = options;
  const [seen, setSeen] = useState<{ monitors: Monitor[]; announcement: string | null }>({
    monitors: EMPTY,
    announcement: null,
  });

  const query = useQuery({
    queryKey: monitorsQueryKey,
    queryFn: ({ signal }) => fetchMonitors(signal),
    // The stream, not a timer, is what keeps this fresh. Polling on top of it
    // would double the load and reintroduce exactly the lag SSE removes.
    refetchInterval: false,
    staleTime: Infinity,
  });

  const connection = useMemo(() => {
    const create: EventSourceFactory =
      createEventSource ?? ((url) => new EventSource(url, { withCredentials: true }));
    return new LiveConnection({ url: streamUrl, create });
  }, [createEventSource, streamUrl]);

  const status = useSyncExternalStore(
    connection.subscribe,
    connection.getStatus,
    () => "connecting" as ConnectionStatus,
  );

  useEffect(() => {
    // Observe every connection edge, even when React batches offline -> live
    // into one render. Consume the cache at that edge: an already queued
    // transition, or a refetch completed while offline, must not replay when
    // the stream returns. The first new frame after reconnect is still news.
    const stopAnnouncements = connection.subscribe(() => {
      setSeen({
        monitors: queryClient.getQueryData<Monitor[]>(monitorsQueryKey) ?? EMPTY,
        announcement: null,
      });
    });
    const stop = connection.onFrame((frame) => {
      const event = parseEvent(frame.type, frame.data);
      if (event === null) return;

      if (event.kind === "hello") {
        // Size the silence watchdog from what this server actually promises,
        // rather than a constant that a change on the server would silently
        // invalidate.
        if (event.pingIntervalMs !== null) connection.setPingInterval(event.pingIntervalMs);
        // The server says this client missed events while it was away. The
        // held list is now provably incomplete, so it is refetched rather
        // than patched — a hole cannot be filled by the frames that follow.
        if (event.gap) void queryClient.invalidateQueries({ queryKey: monitorsQueryKey });
        return;
      }
      if (event.kind === "ping") {
        // Nothing to apply. The connection already treated its arrival as
        // proof of life; there is no state in it.
        return;
      }
      if (event.kind === "lagged") {
        // Same reasoning: the server dropped frames for this subscriber.
        void queryClient.invalidateQueries({ queryKey: monitorsQueryKey });
        return;
      }

      /*
       * An event about a monitor the list has never heard of is news, not
       * noise. `applyHeartbeat`/`applyStatus` return the list untouched when
       * no row matches, which is right for them (they are pure folds over a
       * list) but wrong as a final answer: the commonest cause of an unknown
       * id is a monitor that was just created — in this tab or another one —
       * and dropping the frame leaves the screen claiming it does not exist
       * until someone reloads. So the miss triggers a refetch instead.
       *
       * Throttled, because "unknown id" is also what a burst of frames for a
       * freshly added monitor looks like, and one refetch per heartbeat would
       * turn a stream into a polling loop. One refetch per window is enough:
       * it is the whole list that comes back, not just the one row.
       */
      const current = queryClient.getQueryData<Monitor[]>(monitorsQueryKey);
      const known = current?.some((m) => m.id === event.monitorId) ?? false;
      if (!known) {
        const now = Date.now();
        if (now - lastResyncRef.current >= RESYNC_THROTTLE_MS) {
          lastResyncRef.current = now;
          /*
           * Cancel before invalidating. An invalidation on its own does not
           * restart a fetch that has never produced data, so a frame that
           * arrives while the very first list request is still in flight
           * would invalidate nothing and the new monitor would stay missing
           * until the stale in-flight response landed without it.
           */
          void queryClient
            .cancelQueries({ queryKey: monitorsQueryKey }, { silent: true })
            .then(() => queryClient.invalidateQueries({ queryKey: monitorsQueryKey }));
        }
        return;
      }

      queryClient.setQueryData<Monitor[]>(monitorsQueryKey, (held) => {
        if (held === undefined) return held;
        return event.kind === "heartbeat"
          ? applyHeartbeat(held, event)
          : applyStatus(held, event);
      });
    });

    connection.start();
    return () => {
      stopAnnouncements();
      stop();
      connection.stop();
    };
  }, [connection, queryClient]);

  const monitors = query.data ?? EMPTY;

  // The announcement depends on the *previous* list, which only this hook
  // sees. This is React's "adjust state while rendering" pattern rather than
  // an effect: the sentence is computed in the same render as the list it
  // describes, so a screen reader never hears an announcement that is one
  // render behind what is on screen. An effect would also be a setState inside
  // an effect, which the linter rejects for exactly this reason.
  // Silence is not a retraction of words a screen reader already spoke.
  // ConnectionBadge separately announces the loss of trustworthy live data.
  if (seen.monitors !== monitors) {
    setSeen({
      monitors,
      announcement: status === "live" ? describeTransitions(seen.monitors, monitors) : null,
    });
  }

  return {
    monitors,
    status,
    reconnect: connection.reconnect,
    loading: query.isPending,
    error: query.error instanceof Error ? query.error : null,
    announcement: status === "live" ? seen.announcement : null,
  };
}
