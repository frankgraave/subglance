import { memo } from "react";
import { StateChip, StatusChip } from "../components/Chip";
import {
  describeDelivery,
  describeDestination,
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
 * **Every action names the channel it operates.** Four rows of buttons all
 * called "Test" is a list a screen reader user cannot navigate and a
 * voice-control user cannot address. It is an `aria-label` rather than a
 * visually hidden span because the accessible name concatenates descendant
 * text with no separator, so a hidden span beside the word produces
 * "TestSlack #ops" — and the visible word stays a prefix of the name, which is
 * what WCAG 2.5.3 (label in name) asks for.
 *
 * **Delivery state is never colour alone.** Each of the three states carries a
 * word, and the untested state is a dashed `StateChip` — the register this
 * product already uses for "about the data" rather than "the data" — so it can
 * be told apart from both green and red with the stylesheet off.
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

  return (
    <li className="inv-row" data-disabled={channel.enabled ? "false" : "true"}>
      <div className="inv-line">
        <div className="inv-main">
          <span className="inv-name">
            {channel.name}
            {!channel.enabled && (
              /* A configuration state, not a health state, so no status
                 colour: a dashed chip, which already means "about the data"
                 here. A disabled channel delivers nothing, and silence is
                 indistinguishable from working — so it has to say so. */
              <StateChip className="inv-paused-chip">Disabled</StateChip>
            )}
          </span>
          <span className="inv-sub">{destination}</span>
        </div>

        <div className="inv-meta">
          <span className="inv-col inv-col--type">
            <span className="inv-label">Type</span>
            <span className="inv-type">{typeLabel(channel.type)}</span>
          </span>

          <span className="inv-col inv-col--chan">
            <span className="inv-label">Delivery</span>
            {delivery.kind === "unknown" ? (
              /*
               * The honest default, and the most important cell on the page.
               *
               * `GET /channels` carries no delivery history — no counts, no
               * last error, no last-sent time — so nothing here knows whether
               * this channel has ever worked. A green tick would be invented,
               * and inventing it on a monitoring tool is how a channel that
               * has been failing for three days keeps looking fine.
               */
              <StateChip>Not verified</StateChip>
            ) : delivery.kind === "passed" ? (
              <StatusChip status="up">{deliveryWord}</StatusChip>
            ) : (
              <StatusChip status="down">{deliveryWord}</StatusChip>
            )}
          </span>
        </div>

        <div className="inv-acts">
          {onTest !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onTest(channel.id)}
              disabled={testing}
              aria-label={`${testWord} ${label}`}
            >
              {testWord}
            </button>
          )}

          {onEdit !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onEdit(channel.id)}
              aria-label={`Edit ${label}`}
            >
              Edit
            </button>
          )}

          {onSetEnabled !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onSetEnabled(channel.id, !channel.enabled)}
              disabled={toggling}
              /*
               * The state it moves to, not the state it is in. "Disable" on an
               * enabled channel says what pressing does; a label reading
               * "Enabled" would be a status wearing a button's clothes, and
               * the status is already stated in the row above.
               */
              aria-label={`${channel.enabled ? "Disable" : "Enable"} ${label}`}
            >
              {toggling ? "Saving…" : channel.enabled ? "Disable" : "Enable"}
            </button>
          )}

          {onDelete !== undefined && (
            <button
              type="button"
              className="add-button inv-act inv-act--danger"
              onClick={() => onDelete(channel.id)}
              aria-label={`Delete ${label}`}
            >
              Delete
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
            page as the eye rather than a run of unlabelled columns. */}
        {typeLabel(channel.type)} channel, delivering to {destination}.{" "}
        {channel.enabled ? "Enabled" : "Disabled"}. {deliveryWord}.
      </span>
    </li>
  );
}

export const ChannelRow = memo(ChannelRowImpl);
