import { useCallback, useRef, useState } from "react";
import {
  QueryClientProvider,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { MonitorsView } from "./MonitorsView";
import {
  channelsQueryKey,
  checkMonitorNow,
  deleteMonitor,
  fetchInventory,
  fetchMonitorChannels,
  fetchMonitorForEdit,
  inventoryQueryKey,
  patchMonitor,
  setMonitorPaused,
} from "./inventoryApi";
import type {
  CheckOutcome,
  MonitorPatch,
  VersionedMonitor,
} from "./inventoryApi";

/**
 * The inventory's data owner.
 *
 * Polled rather than streamed. The SSE stream carries heartbeats — which is
 * exactly what this page does not draw — and carries nothing about a monitor
 * being renamed, paused elsewhere or deleted, which is exactly what this page
 * does draw. A live stream here would cost a second connection to keep columns
 * current that it cannot report on.
 *
 * Slow on purpose: settings change when a person changes them, so a minute is
 * ample and a self-hosted box does not need this screen polling like a status
 * board.
 */

export const INVENTORY_POLL_MS = 60_000;

export type LiveMonitorsProps = {
  /** Fetcher seams for tests; default to the real endpoints. */
  fetchMonitors?: typeof fetchInventory;
  fetchChannels?: typeof fetchMonitorChannels;
  pause?: typeof setMonitorPaused;
  remove?: typeof deleteMonitor;
  check?: typeof checkMonitorNow;
  patch?: typeof patchMonitor;
  forEdit?: typeof fetchMonitorForEdit;
  /** Opens a monitor's detail view. */
  onOpen?: (id: string) => void;
  /** True when the URL is /monitors/new. */
  createOpen?: boolean;
  onCreateOpenChange?: (open: boolean) => void;
  /** False for a viewer: every write control disappears rather than failing. */
  canWrite?: boolean;
};

export function LiveMonitors({
  fetchMonitors = fetchInventory,
  fetchChannels = fetchMonitorChannels,
  pause = setMonitorPaused,
  remove = deleteMonitor,
  check = checkMonitorNow,
  patch = patchMonitor,
  forEdit = fetchMonitorForEdit,
  onOpen,
  createOpen = false,
  onCreateOpenChange,
  canWrite = true,
}: LiveMonitorsProps) {
  const queryClient = useQueryClient();

  const monitors = useQuery({
    queryKey: inventoryQueryKey,
    queryFn: ({ signal }) => fetchMonitors(signal),
    refetchInterval: INVENTORY_POLL_MS,
    staleTime: 10_000,
  });

  const ids = (monitors.data ?? []).map((monitor) => monitor.id);
  const channels = useQuery({
    queryKey: channelsQueryKey(ids),
    queryFn: ({ signal }) => fetchChannels(ids, signal),
    enabled: ids.length > 0,
    // Slower still: a monitor's channel attachment changes about as often as
    // its name, and this query costs one request per monitor.
    refetchInterval: 5 * 60_000,
    staleTime: 60_000,
  });

  /*
   * Which rows have a write in flight, as sets rather than one id.
   *
   * A single "busyId" is overwritten the moment a second row is pressed, which
   * re-enables the first row's button while its request is still running — and
   * pausing four monitors in a row is the ordinary way to use this page during
   * a deploy, not an edge case.
   */
  const [busyIds, setBusyIds] = useState<ReadonlySet<string>>(() => new Set());
  const [checkingIds, setCheckingIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [checkResults, setCheckResults] = useState<
    Record<string, CheckOutcome>
  >({});
  /*
   * A failed write is remembered per row, not globally.
   *
   * The page can have several writes in flight, so one shared error string
   * would attribute a failure to whichever row happened to be pressed last —
   * and on a page whose job is to be trusted, an error pointing at the wrong
   * monitor is worse than no error.
   */
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  const mark = useCallback(
    (
      setter: (
        update: (current: ReadonlySet<string>) => ReadonlySet<string>,
      ) => void,
      id: string,
      busy: boolean,
    ) => {
      setter((current) => {
        if (current.has(id) === busy) return current;
        const next = new Set(current);
        if (busy) next.add(id);
        else next.delete(id);
        return next;
      });
    },
    [],
  );

  const noteError = useCallback((id: string, error: unknown) => {
    setRowErrors((current) => ({
      ...current,
      [id]:
        error instanceof Error
          ? error.message
          : "the change could not be saved",
    }));
  }, []);

  const clearError = useCallback((id: string) => {
    setRowErrors((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  }, []);

  const pauseMutation = useMutation({
    mutationFn: ({ id, paused }: { id: string; paused: boolean }) =>
      pause(id, paused),
    onError: (error, variables) => noteError(variables.id, error),
    onSettled: (_data, _error, variables) => {
      mark(setBusyIds, variables.id, false);
      /*
       * Refetch rather than flipping the row locally.
       *
       * The optimistic version is tempting, and it is the one place on this
       * page where guessing is unacceptable: "paused" is a claim that SubGlance
       * has stopped watching something, and a row that says so because the
       * browser assumed a request would succeed is the product lying about
       * whether anyone is watching.
       */
      void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => remove(id),
    onError: (error, id) => noteError(id, error),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
    },
  });

  const checkMutation = useMutation({
    mutationFn: (id: string) => check(id),
    onSuccess: (result, id) => {
      setCheckResults((current) => ({ ...current, [id]: result }));
      // A recorded check moved the monitor's real state, so the row has to be
      // re-read; an unrecorded one (paused monitor) changed nothing on the
      // server and refetching would only make the button feel slow.
      if (result.recorded) {
        void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
      }
    },
    onError: (error, id) => noteError(id, error),
    onSettled: (_data, _error, id) => mark(setCheckingIds, id, false),
  });

  const onTogglePaused = useCallback(
    (id: string, paused: boolean) => {
      clearError(id);
      mark(setBusyIds, id, true);
      pauseMutation.mutate({ id, paused });
    },
    [clearError, mark, pauseMutation],
  );

  const onCheckNow = useCallback(
    (id: string) => {
      clearError(id);
      mark(setCheckingIds, id, true);
      checkMutation.mutate(id);
    },
    [clearError, mark, checkMutation],
  );

  const onDelete = useCallback(
    (id: string) => {
      clearError(id);
      deleteMutation.mutate(id);
    },
    [clearError, deleteMutation],
  );

  /*
   * Opening the edit drawer re-reads the monitor, with its ETag.
   *
   * The form is filled from that response rather than from the list row that
   * was clicked, and the same response's ETag is what the save sends back.
   * Those two have to come from one moment: values read a minute ago plus a
   * validator read just now would let a conditional PATCH sail through and
   * overwrite a change the user never saw — a precondition that guards the
   * wrong instant is worse than none, because it looks like it worked.
   */
  const [editing, setEditing] = useState<VersionedMonitor | null>(null);
  const [editLoadError, setEditLoadError] = useState<string | null>(null);
  /*
   * A token for the edit session currently on screen.
   *
   * Every open, close and completed save advances it, and a request only
   * writes its result if the token has not moved since it started. Without
   * that, pressing Edit on A and then on B before A's read lands lets A's
   * monitor — or A's error — replace B's drawer, so the form would show one
   * monitor's values under another monitor's title and save them with a third
   * monitor's ETag. It is a ref rather than state because nothing renders
   * from it, and because the check has to see the value at the moment the
   * promise settles rather than the one captured when it started.
   */
  const editSession = useRef(0);

  const onEdit = useCallback(
    (id: string) => {
      clearError(id);
      setEditLoadError(null);
      setEditing(null);
      const session = (editSession.current += 1);
      void (async () => {
        try {
          const loaded = await forEdit(id);
          if (editSession.current !== session) return;
          setEditing(loaded);
        } catch (error) {
          if (editSession.current !== session) return;
          setEditLoadError(
            error instanceof Error
              ? error.message
              : "the monitor could not be loaded for editing",
          );
        }
      })();
    },
    [clearError, forEdit],
  );

  const closeEdit = useCallback(() => {
    editSession.current += 1;
    setEditing(null);
    setEditLoadError(null);
  }, []);

  /*
   * Saving sends the validator that came with the values on screen.
   *
   * Without one the endpoint is last-write-wins, and two people tidying the
   * same inventory is exactly the situation this page creates. A 412 comes
   * back as the server's own sentence, which tells the user to re-open the
   * row — rather than the edit silently winning or silently vanishing.
   */
  const onSave = useCallback(
    async (id: string, body: MonitorPatch) => {
      clearError(id);
      const session = editSession.current;
      await patch(id, body, editing?.etag ?? null);
      await queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
      /*
       * A successful edit invalidates its own ETag: the write bumped
       * `updated_at`, so the stamp in hand is now stale and reusing it would
       * make the next save fail with a conflict about the user's own change.
       *
       * Only if this is still the session that started the save, though. The
       * Cancel button stays live while a save is in flight, so the user can
       * close A and open B before A lands — and closing B's drawer because A
       * finished would throw away edits B had already typed.
       */
      if (editSession.current === session) {
        editSession.current += 1;
        setEditing(null);
      }
    },
    [clearError, editing, patch, queryClient],
  );

  const onCreated = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: inventoryQueryKey });
  }, [queryClient]);

  return (
    <MonitorsView
      monitors={monitors.data ?? []}
      channels={channels.data?.byMonitor ?? {}}
      /*
       * Only claimed once the channel query has actually answered.
       *
       * While it is still loading nothing is truncated — it is unfinished —
       * and a banner saying the page gave up would be wrong for as long as the
       * requests are still in the air.
       */
      channelsTruncated={channels.data?.truncated ?? false}
      loading={monitors.isPending}
      error={monitors.error instanceof Error ? monitors.error : null}
      onOpen={onOpen}
      onTogglePaused={canWrite ? onTogglePaused : undefined}
      onCheckNow={canWrite ? onCheckNow : undefined}
      onDelete={canWrite ? onDelete : undefined}
      onSave={canWrite ? onSave : undefined}
      onEdit={canWrite ? onEdit : undefined}
      editing={editing?.monitor ?? null}
      editError={editLoadError}
      onEditClose={closeEdit}
      onCreated={onCreated}
      busyIds={busyIds}
      checkingIds={checkingIds}
      checkResults={checkResults}
      rowErrors={rowErrors}
      createOpen={canWrite && createOpen}
      onCreateOpenChange={canWrite ? onCreateOpenChange : undefined}
    />
  );
}

/** Mounts the inventory with its own query client. */
export function LiveMonitorsRoot(
  props: LiveMonitorsProps & { client?: QueryClient },
) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveMonitors {...rest} />
    </QueryClientProvider>
  );
}
