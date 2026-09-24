import { useCallback, useState } from "react";
import {
  QueryClientProvider,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { NotificationsView } from "./NotificationsView";
import { inventoryQueryKey } from "../monitors/inventoryApi";
import {
  channelsQueryKey,
  createChannel,
  deleteChannel,
  fetchChannels,
  setDefaultChannel,
  testChannel,
  updateChannel,
} from "./channelsApi";
import type { ChannelInput } from "./channelsApi";
import type { Channel, DeliveryState } from "./channels";

/**
 * The notifications page's data owner.
 *
 * Polled slowly, like the inventory: a channel changes when a person changes
 * it, so a minute is ample. The SSE stream carries heartbeats and knows
 * nothing about channels, so a live connection here would cost a second socket
 * to keep a list current that it cannot report on.
 */

export const CHANNELS_POLL_MS = 60_000;

export type LiveNotificationsProps = {
  /** Fetcher seams for tests; default to the real endpoints. */
  list?: typeof fetchChannels;
  create?: typeof createChannel;
  update?: typeof updateChannel;
  remove?: typeof deleteChannel;
  test?: typeof testChannel;
  setDefault?: typeof setDefaultChannel;
  createOpen?: boolean;
  onCreateOpenChange?: (open: boolean) => void;
  /** False for a viewer: every write control disappears rather than failing. */
  canWrite?: boolean;
};

export function LiveNotifications({
  list = fetchChannels,
  create = createChannel,
  update = updateChannel,
  remove = deleteChannel,
  test = testChannel,
  setDefault = setDefaultChannel,
  createOpen = false,
  onCreateOpenChange,
  canWrite = true,
}: LiveNotificationsProps) {
  const queryClient = useQueryClient();

  const channels = useQuery({
    queryKey: channelsQueryKey,
    queryFn: ({ signal }) => list(signal),
    refetchInterval: CHANNELS_POLL_MS,
    staleTime: 10_000,
  });

  /*
   * Test results live here rather than in the query cache.
   *
   * They are not a property of the channel — the server does not store them —
   * they are what *this browser* observed when somebody pressed the button.
   * Putting them in the cache would make a refetch look like it re-verified
   * the channel, and a green tick surviving a reload would be a claim nothing
   * behind it supports.
   */
  const [deliveries, setDeliveries] = useState<Record<string, DeliveryState>>(
    {},
  );
  const [testingIds, setTestingIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  /*
   * A failed write is remembered per row, not globally: a page with two writes
   * in flight would otherwise attribute a failure to whichever row was pressed
   * last, and an error pointing at the wrong channel is worse than none.
   */
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  const mark = useCallback((id: string, busy: boolean) => {
    setTestingIds((current) => {
      if (current.has(id) === busy) return current;
      const next = new Set(current);
      if (busy) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);

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

  const testMutation = useMutation({
    mutationFn: (id: string) => test(id),
    onSuccess: (result, id) => {
      /*
       * The real result, both ways round.
       *
       * A refused delivery is recorded as failed with the upstream's own
       * sentence; there is no path here that turns an unclear answer into a
       * pass. `testChannel` already collapsed "200 with an unreadable body"
       * into a failure for the same reason.
       */
      setDeliveries((current) => ({
        ...current,
        [id]: result.ok
          ? { kind: "passed" }
          : { kind: "failed", error: result.error },
      }));
    },
    onError: (error, id) => {
      /*
       * The request itself never completed — the network died, or the session
       * expired. That is not evidence that the channel is broken, so it is
       * reported as a row error and the delivery state is left alone rather
       * than being marked failed. Claiming a channel is dead because our own
       * request was is how someone ends up re-pasting a working webhook.
       */
      noteError(id, error);
    },
    onSettled: (_data, _error, id) => mark(id, false),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => remove(id),
    onError: (error, id) => noteError(id, error),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: channelsQueryKey });
    },
  });

  const onTest = useCallback(
    (id: string) => {
      clearError(id);
      mark(id, true);
      testMutation.mutate(id);
    },
    [clearError, mark, testMutation],
  );

  const onDelete = useCallback(
    (id: string) => {
      clearError(id);
      /*
       * A deleted channel's test result goes with it.
       *
       * Ids are assigned by the database and a later channel can be given a
       * freed one; a stale "Test delivered" surviving under a reused id would
       * decorate a brand-new, entirely unverified channel with somebody else's
       * proof.
       */
      setDeliveries((current) => {
        if (!(id in current)) return current;
        const next = { ...current };
        delete next[id];
        return next;
      });
      deleteMutation.mutate(id);
    },
    [clearError, deleteMutation],
  );

  /*
   * Enabling and disabling, which the page could report but not change.
   *
   * The PUT carries the channel back as it was read, with only `enabled`
   * different — and that is safe because of something verified in the Go
   * handler rather than assumed: `handleUpdateChannel` treats a config value
   * that still equals its own mask as "unchanged" and substitutes the stored
   * secret. Sending the masked config back therefore preserves the
   * credential. Without that rule this round trip would quietly destroy every
   * webhook token it touched, so it is the reason this is a whole-object PUT
   * rather than a reason to avoid one.
   *
   * The delivery result is deliberately left alone: turning a channel off does
   * not make its last test untrue, and the configuration has not changed.
   */
  const [togglingIds, setTogglingIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );

  const enableMutation = useMutation({
    mutationFn: ({
      channel,
      enabled,
    }: {
      channel: Channel;
      enabled: boolean;
    }) =>
      update(channel.id, {
        name: channel.name,
        type: channel.type,
        config: channel.config,
        enabled,
      }),
    onSettled: async (_data, _error, { channel }) => {
      setTogglingIds((current) => {
        if (!current.has(channel.id)) return current;
        const next = new Set(current);
        next.delete(channel.id);
        return next;
      });
      await queryClient.invalidateQueries({ queryKey: channelsQueryKey });
    },
    onError: (error, { channel }) => noteError(channel.id, error),
  });

  const onSetEnabled = useCallback(
    (id: string, enabled: boolean) => {
      const channel = (channels.data ?? []).find((c) => c.id === id);
      if (channel === undefined) return;
      clearError(id);
      setTogglingIds((current) => new Set(current).add(id));
      enableMutation.mutate({ channel, enabled });
    },
    [channels.data, clearError, enableMutation],
  );

  /*
   * Moving the default is one request; clearing it names the channel that
   * holds it now. Either way the list is refetched rather than patched: the
   * flag moves between two rows, and only the server knows where it landed.
   */
  const [defaultError, setDefaultError] = useState<string | null>(null);
  const defaultMutation = useMutation({
    mutationFn: async (id: string | null) => {
      if (id !== null) return setDefault(id, true);
      const current = (channels.data ?? []).find((c) => c.isDefault);
      if (current !== undefined) await setDefault(current.id, false);
    },
    onMutate: () => setDefaultError(null),
    onError: (error) =>
      setDefaultError(
        error instanceof Error
          ? error.message
          : "the default channel could not be changed",
      ),
    // The inventory names the default beside monitors with no channels of
    // their own, so it is stale the moment the default moves.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: channelsQueryKey }),
        queryClient.invalidateQueries({ queryKey: inventoryQueryKey }),
      ]),
  });
  const onSetDefault = useCallback(
    (id: string | null) => defaultMutation.mutate(id),
    [defaultMutation],
  );

  const onSave = useCallback(
    async (id: string | null, input: ChannelInput) => {
      if (id !== null) clearError(id);
      if (id === null) await create(input);
      else await update(id, input);
      await queryClient.invalidateQueries({ queryKey: channelsQueryKey });
      /*
       * An edited channel loses its test result.
       *
       * The result was about the configuration that was stored a moment ago.
       * Someone who just replaced a revoked Telegram token would otherwise see
       * the old red "Test failed" beside the new token, or worse, an old green
       * tick beside a credential nothing has ever exercised.
       */
      if (id !== null) {
        setDeliveries((current) => {
          if (!(id in current)) return current;
          const next = { ...current };
          delete next[id];
          return next;
        });
      }
    },
    [clearError, create, queryClient, update],
  );

  return (
    <NotificationsView
      channels={channels.data ?? []}
      loading={channels.isPending}
      error={channels.error instanceof Error ? channels.error : null}
      deliveries={deliveries}
      testingIds={testingIds}
      rowErrors={rowErrors}
      /*
       * A test is a write: it sends a real message to somebody else's inbox,
       * and the server guards it with `accessWrite` for exactly that reason. A
       * viewer offered the button would get a 403, which reads as a broken
       * instance rather than as a permission they do not have.
       */
      onTest={canWrite ? onTest : undefined}
      onDelete={canWrite ? onDelete : undefined}
      onSetEnabled={canWrite ? onSetEnabled : undefined}
      togglingIds={togglingIds}
      onSetDefault={canWrite ? onSetDefault : undefined}
      savingDefault={defaultMutation.isPending}
      defaultError={defaultError}
      onSave={canWrite ? onSave : undefined}
      createOpen={canWrite && createOpen}
      onCreateOpenChange={canWrite ? onCreateOpenChange : undefined}
    />
  );
}

/** Mounts the notifications page with its own query client. */
export function LiveNotificationsRoot(
  props: LiveNotificationsProps & { client?: QueryClient },
) {
  const { client, ...rest } = props;
  const [fallback] = useState(createQueryClient);
  return (
    <QueryClientProvider client={client ?? fallback}>
      <LiveNotifications {...rest} />
    </QueryClientProvider>
  );
}
