import { useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { useQuery } from "@tanstack/react-query";
import { createQueryClient } from "./queryClient";
import { useLiveMonitors } from "./useLiveMonitors";
import { useNow } from "./useNow";
import { MonitorDetail } from "../monitors/MonitorDetail";
import { detailQueryKey, fetchMonitorDetail } from "../monitors/detail";
import type { LiveOptions } from "./useLiveMonitors";

/**
 * The detail screen's data owner.
 *
 * It subscribes to the *same* live list the dashboard uses, and picks its
 * monitor out of it. That is the whole design decision on this screen: the
 * monitor's status, latency and heartbeats keep arriving over the existing
 * SSE stream with no new transport, no second subscription and no chance of
 * the two screens disagreeing. Opening a monitor costs one extra request —
 * for uptime windows and incident history, which the list endpoint does not
 * carry — and nothing else.
 *
 * The alternative, fetching GET /api/v1/monitors/:id, was rejected: it would
 * return a snapshot that the stream does not update, so the heartbeat bar
 * would advance while the status line beside it stayed frozen.
 */

export type LiveMonitorDetailProps = LiveOptions & {
  id: string;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
  onBack?: () => void;
};

export function LiveMonitorDetail({ id, beatWidth, onBack, ...live }: LiveMonitorDetailProps) {
  const { monitors, status, loading, error } = useLiveMonitors(live);
  const now = useNow();

  const detail = useQuery({
    queryKey: detailQueryKey(id),
    queryFn: ({ signal }) => fetchMonitorDetail(id, signal),
    /*
     * Unlike the monitor list, this is not fed by the stream: incidents open
     * and close without a frame that says so. A minute is short enough that
     * an incident opened while you watch appears on its own, and long enough
     * that leaving the page open is not a poll loop.
     */
    refetchInterval: 60_000,
    staleTime: 30_000,
  });

  const monitor = monitors.find((m) => m.id === id);

  if (monitor === undefined) {
    /*
     * Three different situations share this branch, and they need three
     * different sentences. Guessing "not found" while the list is still
     * loading is the common version of this bug, and it tells the user their
     * monitor was deleted when it was not.
     */
    const notice =
      error !== null
        ? `Could not load monitors: ${error.message}`
        : loading
          ? "Loading monitor…"
          : "That monitor does not exist, or has been deleted.";
    return (
      <section className="mon-detail mon-detail--missing">
        <nav className="mon-detail-nav">
          <button type="button" className="mon-detail-back" onClick={onBack}>
            <span aria-hidden="true">←</span> All monitors
          </button>
        </nav>
        <p role={loading ? undefined : "alert"} className="mon-detail-empty">
          {notice}
        </p>
      </section>
    );
  }

  return (
    <MonitorDetail
      monitor={monitor}
      windows={detail.data?.windows ?? []}
      incidents={detail.data?.incidents ?? []}
      now={now}
      loading={detail.isPending}
      error={detail.error instanceof Error ? detail.error : null}
      onBack={onBack}
      beatWidth={beatWidth}
      stale={status === "offline"}
    />
  );
}

/** Mounts the detail screen with its own query client. */
export function LiveMonitorDetailRoot(props: LiveMonitorDetailProps & { client?: QueryClient }) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveMonitorDetail {...rest} />
    </QueryClientProvider>
  );
}
