import { useCallback, useRef, useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createQueryClient } from "./queryClient";
import { checkMonitorNow, type CheckOutcome } from "../monitors/inventoryApi";
import { useLiveMonitors } from "./useLiveMonitors";
import { useNow } from "./useNow";
import { EditMonitorDrawer } from "../monitors/EditMonitorDrawer";
import { MonitorDetail } from "../monitors/MonitorDetail";
import { detailQueryKey, fetchMonitorDetail } from "../monitors/detail";
import { fetchResponseHistory, responseHistoryQueryKey } from "../monitors/responseHistoryApi";
import { fetchLatency, latencyQueryKey, type LatencyWindow } from "../monitors/latency";
import { ackIncident } from "../incidents/api";
import { openIncidentsQueryKey } from "../incidents/api";
import type { LiveOptions } from "./useLiveMonitors";

/**
 * The detail screen's data owner.
 *
 * It subscribes to the *same* live list the dashboard uses, and picks its
 * monitor out of it. That is the whole design decision on this screen: the
 * monitor's status, latency and heartbeats keep arriving over the existing
 * SSE stream with no new transport or second subscription. Uptime and
 * incidents have their own detail reads. HTTP failure diagnostics additionally
 * use the raw heartbeat endpoint, because bulk beats intentionally omit bodies.
 *
 * The alternative, fetching GET /api/v1/monitors/:id, was rejected: it would
 * return a snapshot that the stream does not update, so the heartbeat bar
 * would advance while the status line beside it stayed frozen.
 */

export type LiveMonitorDetailProps = LiveOptions & {
  id: string;
  /** Heartbeat width for environments without layout, such as jsdom. */
  beatWidth?: number;
  onBack?: () => void;
  /** Ack seam for tests; defaults to the real endpoint. */
  ack?: typeof ackIncident;
  check?: typeof checkMonitorNow;
  /** The shell supplies the authenticated user's write permission. */
  canWrite?: boolean;
};

export function LiveMonitorDetail({
  id,
  beatWidth,
  onBack,
  ack = ackIncident,
  check = checkMonitorNow,
  canWrite = true,
  ...live
}: LiveMonitorDetailProps) {
  const { monitors, status, loading, error } = useLiveMonitors(live);
  const now = useNow();
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<string | null>(null);
  // Leaving a monitor ends its edit session, even when the next id is missing.
  // Clear before children commit so returning cannot reopen or focus a drawer.
  if (editingId !== null && editingId !== id) setEditingId(null);

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

  /*
   * Acknowledging, from the screen an alert link lands on.
   *
   * Both caches are invalidated afterwards, never patched: the claim an acked
   * row makes is "a human has seen this", and a row that says so because the
   * browser assumed a request would succeed is the product inventing the one
   * fact this control exists to record. The incidents screen holds the same
   * incident, so it is refetched too — two screens disagreeing about whether
   * somebody is on it is exactly the confusion ack is meant to end.
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
  const ackMutation = useMutation({
    mutationFn: (incidentId: string) => ack(incidentId),
    onSettled: (_data, _error, incidentId) => {
      markAcking(incidentId, false);
      void queryClient.invalidateQueries({ queryKey: detailQueryKey(id) });
      void queryClient.invalidateQueries({ queryKey: openIncidentsQueryKey });
    },
  });
  const onAck = useCallback(
    (incidentId: string) => {
      markAcking(incidentId, true);
      ackMutation.mutate(incidentId);
    },
    [ackMutation, markAcking],
  );

  const monitor = monitors.find((m) => m.id === id);
  const responseHistory = useQuery({
    queryKey: responseHistoryQueryKey(id),
    queryFn: ({ signal }) => fetchResponseHistory(id, signal),
    enabled: monitor?.type === "http",
    // SSE and bulk beat bars omit response bodies. Keep a separate bounded
    // raw-history query; never infer historical reasons from today's status.
    refetchInterval: 60_000,
    staleTime: 30_000,
  });

  /*
   * The latency chart's window, and its series. Push monitors are reported to
   * rather than probed, so they have no latency and no request is made.
   *
   * The placeholder keeps the old window on screen while the new one
   * loads, but only for the same monitor: the placeholder is dropped when the
   * id changes, so monitor B never shows A's line under its own title.
   */
  const [latencyWindow, setLatencyWindow] = useState<LatencyWindow>("24h");
  const latency = useQuery({
    queryKey: latencyQueryKey(id, latencyWindow),
    queryFn: ({ signal }) => fetchLatency(id, latencyWindow, signal),
    enabled: monitor !== undefined && monitor.push === undefined,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === id ? previous : undefined,
    refetchInterval: 60_000,
    staleTime: 30_000,
  });

  // Key by monitor, not by the most recent click: routing to B while A checks
  // must not put A's result under B's title. The ref closes the same-tick gap
  // before React has painted the disabled button.
  const checksInFlight = useRef(new Set<string>());
  const [checks, setChecks] = useState<Record<string, {
    checking: boolean; result?: CheckOutcome; error?: Error;
  }>>({});
  const checkMutation = useMutation({
    mutationFn: (monitorId: string) => check(monitorId),
    retry: false,
    onSuccess: (result, monitorId) => {
      setChecks((current) => ({ ...current, [monitorId]: { checking: false, result } }));
      // A paused probe is diagnostic only. Do not manufacture a heartbeat or
      // refetch the dashboard as though the server had recorded one.
      if (result.recorded) {
        void queryClient.invalidateQueries({ queryKey: ["monitors"] });
        void queryClient.invalidateQueries({ queryKey: detailQueryKey(monitorId) });
        void queryClient.invalidateQueries({ queryKey: openIncidentsQueryKey });
      }
    },
    onError: (error, monitorId) => {
      setChecks((current) => ({ ...current, [monitorId]: { checking: false, error } }));
    },
    onSettled: (_data, _error, monitorId) => { checksInFlight.current.delete(monitorId); },
  });
  const onCheckNow = () => {
    if (!canWrite || monitor === undefined || monitor.push !== undefined || checksInFlight.current.has(id)) return;
    checksInFlight.current.add(id);
    setChecks((current) => ({ ...current, [id]: { checking: true } }));
    checkMutation.mutate(id);
  };

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
    <>
    {canWrite && editingId === id && <EditMonitorDrawer key={id} id={id} onClose={() => setEditingId(null)} />}
    <MonitorDetail
      monitor={monitor}
      windows={detail.data?.windows ?? []}
      incidents={detail.data?.incidents ?? []}
      responseHistory={monitor.type === "http" ? {
        heartbeats: responseHistory.data ?? [],
        loading: responseHistory.isPending,
        error: responseHistory.error instanceof Error ? responseHistory.error : null,
      } : undefined}
      latency={monitor.push === undefined ? {
        window: latencyWindow,
        onWindowChange: setLatencyWindow,
        series: latency.data,
        loading: latency.isPending,
        error: latency.error instanceof Error ? latency.error : null,
        refreshing: latency.isPlaceholderData,
        width: beatWidth,
      } : undefined}
      now={now}
      loading={detail.isPending}
      error={detail.error instanceof Error ? detail.error : null}
      onBack={onBack}
      beatWidth={beatWidth}
      /*
       * Anything other than a confirmed live stream is stale. The monitor can
       * come out of the shared cache while the new SSE connection is still
       * "connecting", and painting that as live states old data as current
       * truth. Only "live" earns the live colours.
       */
      stale={status !== "live"}
      onAck={canWrite ? onAck : undefined}
      onEdit={canWrite ? () => setEditingId(id) : undefined}
      onCheckNow={canWrite ? onCheckNow : undefined}
      checking={checks[id]?.checking ?? false}
      checkResult={checks[id]?.result}
      checkError={checks[id]?.error ?? null}
      ackingIds={ackingIds}
      ackError={ackMutation.error instanceof Error ? ackMutation.error : null}
    />
    </>
  );
}

/** Mounts the detail screen with its own query client. */
export function LiveMonitorDetailRoot(
  props: LiveMonitorDetailProps & { client?: QueryClient },
) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveMonitorDetail {...rest} />
    </QueryClientProvider>
  );
}
