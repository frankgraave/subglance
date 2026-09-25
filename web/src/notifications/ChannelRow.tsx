import { memo } from "react";
import { StateChip, StatusChip } from "../components/Chip";
import { Value } from "../components/Value";
import {
  IconBell,
  IconBellOff,
  IconPencil,
  IconTrash,
} from "../components/icons";
import { formatMoment } from "../monitors/detail";
import {
  describeDelivery,
  describeDestination,
  describeQuietHours,
  quietChip,
  typeLabel,
} from "./channels";
import type { Channel, DeliveryState } from "./channels";

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
 * `.inv-act--icon`, borrowed rather than re-specified so the two lists cannot
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
 * **Delivery is not a column any more.** It used to be one, and it read "Not
 * verified" in a dashed chip on every row, always, because the API carries no
 * delivery history at all. A value that is identical on every row and
 * structurally incapable of differing carries no information, and this one was
 * taking the most visual weight in the row to carry it. The fact is now stated
 * once for the whole list, where it belongs — it is a property of the product,
 * not of a channel — and the row prints a delivery state only when a test has
 * actually produced one, as a chip on the name line beside the row's other
 * state. Nothing was softened: see `NotificationsView`'s legend, which still
 * says a channel failing for three days looks exactly like one never needed.
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
  const testWord = testing ? "Sending test…" : "Send test";
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
           * When the channel was added (SUB-138).
           *
           * The column that used to sit here said "Not verified" in a dashed
           * chip on every row, always — the API carries no delivery history,
           * so it was structurally incapable of ever differing. A value
           * identical on every row is not a column; it is a caption repeated N
           * times, and it was taking the most visual weight in the row to be
           * one. That fact is now stated once above the list.
           *
           * This is the honest thing to put in its place: `created_at` is
           * already on the wire, already parsed, and was being thrown away.
           * It genuinely differs per row, and it is the one date that bears on
           * the page's own argument — a channel added eleven months ago and
           * never tested is a different risk from one added this morning, and
           * until now nothing on the screen let you tell those apart.
           *
           * Absolute, not "3 days ago", matching the monitor detail view: this
           * is a date you line up against when someone changed something, and
           * a relative stamp goes stale while the page sits open.
           *
           * A server that sends no timestamp gets an empty `Value`, which
           * renders in the tone that means "no measurement" — not a zero, and
           * not an invented date.
           */}
          <span className="inv-col inv-col--added">
            <span className="inv-label">Added</span>
            <Value value={channel.createdAt}>
              {formatMoment(channel.createdAt)}
            </Value>
          </span>
        </div>

        <div className="inv-acts">
          {onTest !== undefined && (
            <button
              type="button"
              className="add-button inv-act nt-act-test"
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
              className="inv-act inv-act--icon nt-act-toggle"
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
              className="inv-act inv-act--icon"
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
              className="inv-act inv-act--icon inv-act--danger"
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
            delivery state is spoken on every row, including the unknown one:
            the eye has the list's legend a few centimetres above and in view,
            and a reader moving item by item through a list does not. */}
        {typeLabel(channel.type)} channel, delivering to {destination}.{" "}
        {channel.isDefault ? "The default channel. " : ""}
        {channel.quietHours !== null
          ? `${describeQuietHours(channel.quietHours).replace(/^q/, "Q")}. `
          : ""}
        {channel.enabled ? "Enabled" : "Disabled"}. {deliveryWord}.
      </span>
    </li>
  );
}

export const ChannelRow = memo(ChannelRowImpl);
