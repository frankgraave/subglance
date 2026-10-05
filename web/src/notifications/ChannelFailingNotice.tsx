import { useQuery } from "@tanstack/react-query";
import { channelsQueryKey, fetchChannels } from "./channelsApi";
import { describeFailureNotice, failingChannels } from "./channels";
import { NOTIFICATIONS_PATH } from "../shell/route";

/**
 * The dashboard's line for "a channel has stopped delivering alerts".
 *
 * Design reasoning:
 *
 * 1. **On the dashboard, not in the masthead.** The masthead holds what does
 *    something on every screen (AGENTS.md); this line is a fact about the
 *    estate, and the dashboard is the screen that is watched. It stands with
 *    the other statements about whether the screen can be trusted, above the
 *    monitors, because an outage on it may have been told to nobody.
 * 2. **Warn, not down.** A dead channel is not an outage of anything watched,
 *    the same argument the host-offline line makes. The shared `.warn-note`
 *    draws it, so the dashboard grows no new surface.
 * 3. **It names the channel and what was done about it**: reported through
 *    another channel, or that there is none to report it through, which is
 *    the case where this line is the only place it is said.
 * 4. **Only what the server vouches for.** A channel list that failed to load
 *    draws nothing, and so does one whose latest refetch failed, even with an
 *    older answer still cached: this line never guesses, in either direction.
 * 5. **A real link** to the notifications screen, for the reasons
 *    `MonitorLink` gives; the handler only swaps the page load for a route
 *    change on a plain left click.
 */
export function ChannelFailingNotice({
  onOpen,
}: {
  /** Opens the notifications screen without a page load. */
  onOpen?: () => void;
}) {
  const { data, isError } = useQuery({
    queryKey: channelsQueryKey,
    queryFn: ({ signal }) => fetchChannels(signal),
    refetchInterval: 60_000,
  });
  const failing = data === undefined ? [] : failingChannels(data);
  // A refetch that failed keeps the last answer in `data`; the line says
  // only what the latest answer vouches for (point 4).
  if (data === undefined || isError || failing.length === 0) return null;

  const names = new Map(data.map((c) => [c.id, c.name]));
  const first = failing[0];
  const text =
    failing.length === 1
      ? `Alerts to ${first.name} are not arriving: one gave up and none has arrived since.`
      : `Alerts to ${failing.length} channels are not arriving: ${failing.map((c) => c.name).join(", ")}.`;
  const notice = failing.length === 1 ? describeFailureNotice(first.history, names) : null;
  const unreported = failing.filter((c) => c.history.notice === "none").length;
  const tail =
    notice ??
    (unreported === 0
      ? null
      : `${unreported} of them cannot be reported through another channel`);

  return (
    <p role="status" className="warn-note nt-failing">
      {text}
      {tail === null ? null : ` ${tail}.`}{" "}
      <a
        href={NOTIFICATIONS_PATH}
        onClick={(event) => {
          if (
            onOpen === undefined ||
            event.defaultPrevented ||
            event.button !== 0 ||
            event.metaKey ||
            event.ctrlKey ||
            event.shiftKey ||
            event.altKey
          ) {
            return;
          }
          event.preventDefault();
          onOpen();
        }}
      >
        Open notifications
      </a>
    </p>
  );
}
