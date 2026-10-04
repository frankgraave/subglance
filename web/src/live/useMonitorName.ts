import { useCallback } from "react";
import { useQuery, type QueryClient } from "@tanstack/react-query";
import { fetchMonitors, monitorsQueryKey } from "./api";
import type { Monitor } from "../monitors/types";

/**
 * A monitor's name, read from the shared monitor list for the masthead's
 * title (SUB-207).
 *
 * The shell titles every page, and on a monitor's page the title is the
 * monitor's name. `App` knows only the id; the name is in the list the
 * dashboard and the detail screen already share. Reading the whole list here
 * would re-render the shell on every heartbeat, which is why the tab used to
 * say "Monitor" instead (SUB-182). `select` is what makes it cheap: the
 * observer is notified only when the selected string changes, so a stream of
 * heartbeats for the same monitor re-renders nothing above the screen.
 *
 * The same key and fetcher as `useLiveMonitors`, so on a cold load of
 * `/monitors/{id}` the two observers share one request rather than racing
 * two. `null` while the list is loading or when the id names no monitor; the
 * caller says "Monitor" then, the word the screen itself uses for that state.
 */
export function useMonitorName(client: QueryClient, id: string | null): string | null {
  const select = useCallback(
    (monitors: Monitor[]) => monitors.find((m) => m.id === id)?.name ?? null,
    [id],
  );
  const { data } = useQuery(
    {
      queryKey: monitorsQueryKey,
      queryFn: ({ signal }) => fetchMonitors(signal),
      staleTime: Infinity,
      enabled: id !== null,
      select,
    },
    client,
  );
  return id === null ? null : (data ?? null);
}
