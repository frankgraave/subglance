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

import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
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

export function useLiveMonitors(options: LiveOptions = {}): UseLiveMonitors {
  const queryClient = useQueryClient();
  const { streamUrl = "/api/v1/stream", createEventSource } = options;

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

      queryClient.setQueryData<Monitor[]>(monitorsQueryKey, (current) => {
        if (current === undefined) return current;
        return event.kind === "heartbeat"
          ? applyHeartbeat(current, event)
          : applyStatus(current, event);
      });
    });

    connection.start();
    return () => {
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
  const [seen, setSeen] = useState<{ monitors: Monitor[]; announcement: string | null }>({
    monitors: EMPTY,
    announcement: null,
  });
  if (seen.monitors !== monitors) {
    setSeen({ monitors, announcement: describeTransitions(seen.monitors, monitors) });
  }

  return {
    monitors,
    status,
    reconnect: connection.reconnect,
    loading: query.isPending,
    error: query.error instanceof Error ? query.error : null,
    announcement: seen.announcement,
  };
}
