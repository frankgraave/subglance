import { memo } from "react";
import { StateChip, StatusChip } from "../components/Chip";
import {
  IconBell,
  IconBellOff,
  IconPencil,
  IconTrash,
} from "../components/icons";
import {
  describeDelivery,
  describeDestination,
  describeHistory,
  describeQuietHours,
  historyChip,
  historyMoment,
  quietChip,
  typeLabel,
} from "./channels";
import type { Channel, DeliveryState } from "./channels";

/*
 * The test button's in-flight label. Also the width the button reserves at
 * rest (`data-reserve`, notifications.css), so starting a test does not move
 * the row's other actions.
 */
const TEST_BUSY_WORD = "Sending test…";

/**
 * One channel as one row.
 *
 * **Two surfaces, not three (commit 1eaa151).** The `Card` on the screen is
 * the first; this row, which carries its own border, radius and background, is
 * the second. There is no wrapper between them — the row is a direct child of
 * the card's list.
 *
 * **It is the monitors inventory row, and now it looks like one (SUB-138).**
 * A channel row and a monitor row are the same object — an identity, a couple
 * of settings columns and inline actions — and they already shared `.inv-row`.
 * What they did not share was the actions: this row drew four full outlined
 * buttons, "Send test | Edit | Disable | Delete", with Delete in red on every
 * single row, while the monitors row two files away drew four quiet 26px
 * glyphs. Four bordered rectangles per row is a wall at five channels and the
 * loudest thing on a screen whose subject is the two lines of text to their
 * left. Edit, disable and delete are now glyphs on the inventory's own
 * `.icon-button`, borrowed rather than re-specified so the two lists cannot
 * drift apart again.
 *
 * **Send test stays a word, and that is the one deliberate departure.** It is
 * this page's primary verb: the whole argument of the screen is that nothing
 * here is known to work until you press it, so it is the only control a reader
 * arriving with a broken alert is looking for. It is also the one action that
 * is not in the monitors row's vocabulary — check, pause, edit, delete are
 * learned there, and a fifth unfamiliar glyph would be the one nobody presses.
 * It sends a real message to somebody else's inbox, which is not an act to put
 * behind an unlabelled square. So: one word, three glyphs, and the word is the
 * one that matters.
 *
 * **Disable is a struck-through bell, not a pause (SUB-138).** Pausing a
 * monitor stops SubGlance checking; disabling a channel leaves every check
 * running and stops SubGlance telling anyone. Same-shaped buttons for those
 * two would claim they are the same act.
 *
 * **Delivery is a column again, and now it differs per row (SUB-180).** It
 * was removed when it read "Not verified" on every row, always, because the
 * API carried no delivery history; a value that cannot vary is not a column.
 * The API now sends each channel's record from the outbox, so the column
 * says what happened to the channel's real alerts: Failed, Retrying,
 * Delivered, or "None in 30 days". A test result is a different, narrower
 * claim — about one moment, from this browser — and keeps its own chip on the
 * name line.
 */

export type ChannelRowProps = {
  channel: Channel;
  /** What the last test through this browser reported, if any. */
  delivery: DeliveryState;
  /** True while this row's test delivery is in flight. */
  testing?: boolean;
  /** Sends a real message. Absent for a viewer, who may not write. */
  onTest?: (id: string) => void;
  /** Opens the edit drawer. Absent for a viewer. */
  onEdit?: (id: string) => void;
  /** Asks for the delete confirmation. Absent for a viewer. */
  onDelete?: (id: string) => void;
  /**
   * Turns delivery through this channel on or off. Absent for a viewer.
   *
   * A disabled channel was visible but unchangeable: the row said "Disabled"
   * and offered no way back. Disabling is also the honest alternative to
   * deleting when a webhook is temporarily noisy — it keeps the credentials
   * and the monitor attachments, where deleting destroys both.
   */
  onSetEnabled?: (id: string, enabled: boolean) => void;
  /** True while this row's enable/disable write is in flight. */
  toggling?: boolean;
  /** A write on this row that failed, in the server's own words. */
  rowError?: string | null;
};

function ChannelRowImpl({
  channel,
  delivery,
  testing = false,
  onTest,
  onEdit,
  onDelete,
  onSetEnabled,
  toggling = false,
  rowError = null,
}: ChannelRowProps) {
  const destination = describeDestination(channel);
  const deliveryWord = describeDelivery(delivery);
  /*
   * A switched-off channel with nothing in its window draws no chip. "None
   * in 30 days" is a warning that the channel has proved nothing and wants a
   * test; on a channel that sends nothing by choice the Disabled chip already
   * says why it is quiet, and a second chip saying so again would be the
   * dimmed row's loudest thing. History it does have — say it failed before
   * someone switched it off — still shows.
   */
  const chip =
    !channel.enabled && channel.history.state === "none"
      ? null
      : historyChip(channel.history);
  const moment = historyMoment(channel.history);
  const historyText = describeHistory(channel.history);
  const testWord = testing ? TEST_BUSY_WORD : "Send test";
  const label = `${typeLabel(channel.type)} ${channel.name}`;
  /*
   * The state it moves to, not the state it is in. "Disable" on an enabled
   * channel says what pressing does; a label reading "Enabled" would be a
   * status wearing a button's clothes, and the status is already on the row.
   */
  const toggleWord = toggling
    ? "Saving…"
    : channel.enabled
      ? "Disable"
      : "Enable";

  return (
    <li className="inv-row" data-disabled={channel.enabled ? "false" : "true"}>
      <div className="inv-line">
        <div className="inv-main">
          <span className="inv-name">
            {channel.name}
            {channel.isDefault && (
              /* Configuration, like Disabled beside it, so the same dashed
                 chip. It is on the row as well as in the sentence above the
                 list because deleting or disabling this row changes where
                 every unrouted alert goes, and that should be visible at the
                 place the button is pressed. */
              <StateChip className="inv-paused-chip">Default</StateChip>
            )}
            {channel.quietHours !== null && (
              /* Configuration again, so the dashed chip. It is on the row
                 because it answers "why did nobody hear about this at 03:00"
                 at the place someone goes to look. The title carries the
                 timezone the chip has no room for. */
              <span title={describeQuietHours(channel.quietHours)}>
                <StateChip className="inv-paused-chip">
                  {quietChip(channel.quietHours)}
                </StateChip>
              </span>
            )}
            {!channel.enabled && (
              /* A configuration state, not a health state, so no status
                 colour: a dashed chip, which already means "about the data"
                 here. A disabled channel delivers nothing, and silence is
                 indistinguishable from working — so it has to say so. */
              <StateChip className="inv-paused-chip">Disabled</StateChip>
            )}
            {delivery.kind !== "unknown" && (
              /*
               * A real result, and the only delivery state that is ever drawn
               * on a row. It appears because a test was run and it says what
               * that test found; the sentence underneath says what the result
               * does and does not prove. Never rendered for `unknown`, which
               * is every row on a freshly loaded page — that fact is the
               * list's, not the row's.
               */
              <StatusChip
                className="inv-paused-chip"
                status={delivery.kind === "passed" ? "up" : "down"}
              >
                {deliveryWord}
              </StatusChip>
            )}
          </span>
          <span className="inv-sub">{destination}</span>
        </div>

        <div className="inv-meta">
          <span className="inv-col inv-col--type">
            <span className="inv-label">Type</span>
            <span className="inv-type">{typeLabel(channel.type)}</span>
          </span>

          {/*
           * What happened to the channel's real alerts (SUB-180). It took
           * the Added column's place and its rung-4 width: that column was
           * put here in SUB-138 as the most honest thing available while
           * the API carried no history, and the date it showed bears on the
           * page's question far less than whether alerts arrive. The width
           * is what holds the 638px wrap the row's layout is measured at.
           *
           * The chip is the state, the text beside it the moment it is
           * about. The counts and the error go in the title and in the
           * row's screen-reader sentence, where they have room; a third
           * line on a failing row would make it taller than its neighbours
           * (DESIGN.md §8.10). `null` — a record the server did not send —
           * draws nothing rather than a guess.
           */}
          <span className="inv-col inv-col--delivery" title={historyText}>
            <span className="inv-label">Delivery</span>
            <span className="nt-history">
              {chip !== null &&
                (chip.status === null ? (
                  <StateChip>{chip.word}</StateChip>
                ) : (
                  <StatusChip status={chip.status}>{chip.word}</StatusChip>
                ))}
              {moment !== null && <span className="inv-type">{moment}</span>}
            </span>
          </span>
        </div>

        <div className="inv-acts">
          {onTest !== undefined && (
            <button
              type="button"
              className="button button--compact nt-act-test"
              data-reserve={TEST_BUSY_WORD}
              onClick={() => onTest(channel.id)}
              disabled={testing}
              aria-busy={testing}
              aria-label={`${testWord} ${label}`}
            >
              {testWord}
            </button>
          )}

          {onSetEnabled !== undefined && (
            <button
              type="button"
              className="icon-button nt-act-toggle"
              onClick={() => onSetEnabled(channel.id, !channel.enabled)}
              disabled={toggling}
              aria-busy={toggling}
              aria-label={`${toggleWord} ${label}`}
              /* The word is gone from the face of the button, so the hint a
                 pointer gets is the whole of it — and it repeats the
                 accessible name rather than abbreviating it. */
              title={`${toggleWord} ${label}`}
            >
              {/* Two different shapes, not one shape in two colours: which
                  state the button is in has to survive greyscale. */}
              {channel.enabled ? <IconBellOff /> : <IconBell />}
            </button>
          )}

          {onEdit !== undefined && (
            <button
              type="button"
              className="icon-button"
              onClick={() => onEdit(channel.id)}
              aria-label={`Edit ${label}`}
              title={`Edit ${label}`}
            >
              <IconPencil />
            </button>
          )}

          {onDelete !== undefined && (
            <button
              type="button"
              className="icon-button button--danger"
              onClick={() => onDelete(channel.id)}
              aria-label={`Delete ${label}`}
              title={`Delete ${label}`}
            >
              {/* A bin, matching the monitors row. Destructive in its shape,
                  so it survives greyscale without the word; the pause the
                  word used to buy is bought by the confirmation, which keeps
                  its button disabled until the channel's name is typed. */}
              <IconTrash />
            </button>
          )}
        </div>
      </div>

      {/*
       * The result of the last test, beside the row it is about.
       *
       * Not a toast: "did the fix work" has to stay on screen next to the
       * channel it describes, and a toast takes it away on a timer
       * (DESIGN.md §7.6). The upstream error is printed verbatim — a test that
       * says only "failed" sends you to the server log, which is the exact
       * trip this button exists to save.
       */}
      {delivery.kind !== "unknown" && (
        <p
          className={
            delivery.kind === "failed"
              ? "inv-result inv-result--bad"
              : "inv-result"
          }
          role="status"
        >
          {delivery.kind === "passed"
            ? "The test message was accepted by the far end. That proves this channel can deliver right now; it says nothing about deliveries made before this test."
            : `The test message was not delivered. The far end said: ${delivery.error}`}
        </p>
      )}

      {rowError !== null && (
        <p className="inv-result inv-result--bad" role="alert">
          {rowError}
        </p>
      )}

      <span className="sr-only">
        {/* The row's facts as one sentence, so a screen reader gets the same
            page as the eye rather than a run of unlabelled columns. The
            delivery history is spoken on every row, including an unknown
            one: the eye has the list's legend a few centimetres above and in
            view, and a reader moving item by item through a list does not.
            A test result follows it only when a test produced one. */}
        {typeLabel(channel.type)} channel, delivering to {destination}.{" "}
        {channel.isDefault ? "The default channel. " : ""}
        {channel.quietHours !== null
          ? `${describeQuietHours(channel.quietHours).replace(/^q/, "Q")}. `
          : ""}
        {channel.enabled ? "Enabled" : "Disabled"}. {historyText}.
        {delivery.kind === "unknown" ? "" : ` ${deliveryWord}.`}
      </span>
    </li>
  );
}

export const ChannelRow = memo(ChannelRowImpl);
