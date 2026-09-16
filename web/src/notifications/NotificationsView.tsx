import { useState } from "react";
import { Card } from "../components/Card";
import { Drawer } from "../components/Drawer";
import { StateChip } from "../components/Chip";
import { ChannelRow } from "./ChannelRow";
import { ChannelForm } from "./ChannelForm";
import { describeChannels } from "./channels";
import type { Channel, DeliveryState } from "./channels";
import { DELIVERY_UNKNOWN } from "./channels";
import type { ChannelInput } from "./channelsApi";

/**
 * Notifications, part one: can we reach you at all.
 *
 * The page answers exactly one question — which destinations exist, whether
 * they work, and how to change them. The approved mockup also draws routing,
 * quiet hours, severity floors and a delivery log. None of those have a
 * backend (SUB-124), and a switch that looks like it suppresses alerts at
 * 03:00 while changing nothing is the most dangerous shape of dead UI this
 * product could ship. So they are absent rather than disabled: a disabled
 * control is a feature that looks temporarily unavailable, and these do not
 * exist.
 *
 * Presentational, like `MonitorsView` and `IncidentsView`: it fetches nothing,
 * so a test drives every state from a fixture. `LiveNotifications` owns the
 * data.
 */

export type NotificationsViewProps = {
  channels: readonly Channel[];
  loading?: boolean;
  error?: Error | null;
  /** The last test result per channel id, from this browser only. */
  deliveries?: Readonly<Record<string, DeliveryState>>;
  /** Ids with a test delivery in flight. */
  testingIds?: ReadonlySet<string>;
  /** A failed write per channel id, in the server's own words. */
  rowErrors?: Readonly<Record<string, string>>;
  /** Sends a real message. Absent for a viewer, who may not write. */
  onTest?: (id: string) => void;
  onDelete?: (id: string) => void;
  /** Saves a new or edited channel. Resolves when the server accepted it. */
  onSave?: (id: string | null, input: ChannelInput) => Promise<void>;
  /** True when the URL asked for the create form: /notifications/new. */
  createOpen?: boolean;
  onCreateOpenChange?: (open: boolean) => void;
};

const NO_SET: ReadonlySet<string> = new Set();
const NO_MAP = {};

export function NotificationsView({
  channels,
  loading = false,
  error = null,
  deliveries = NO_MAP,
  testingIds = NO_SET,
  rowErrors = NO_MAP,
  onTest,
  onDelete,
  onSave,
  createOpen = false,
  onCreateOpenChange,
}: NotificationsViewProps) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const editing = channels.find((c) => c.id === editingId) ?? null;
  const deleteTarget = channels.find((c) => c.id === confirming) ?? null;
  const canWrite = onSave !== undefined;

  return (
    <section className="mon-detail inv-screen" aria-label="Notifications">
      <header className="mon-detail-head">
        <h1 className="mon-detail-name">Notifications</h1>
        <p className="mon-detail-note">{describeChannels(channels)}</p>
      </header>

      {error !== null && (
        <p className="inc-notice" role="alert">
          {error.message}
        </p>
      )}

      {/*
       * The page admits what it cannot know.
       *
       * The mockup promises a delivery state on every row, and the ticket asks
       * for three of them. The API supplies none: `GET /channels` returns id,
       * name, type, masked config, enabled and timestamps — no delivery
       * counts, no last error, no last-sent time. Saying so once, here, is
       * what keeps "Not verified" on a row from reading as a bug.
       */}
      <p className="add-help">
        SubGlance cannot yet tell you whether a channel has been delivering.
        The channel API carries no delivery history, so every row starts as{" "}
        <b>Not verified</b> and only a test you run here can change it. A
        channel that has failed every delivery for three days looks exactly the
        same as one that has never been needed — which is why the test button
        is on every row.
      </p>

      <Card
        title="Channels"
        headingLevel={2}
        action={
          onCreateOpenChange === undefined ? undefined : (
            <button
              type="button"
              className="add-button add-button-primary"
              onClick={() => onCreateOpenChange(true)}
            >
              Add channel
            </button>
          )
        }
      >
        {loading || error !== null ? (
          /*
           * Neither loading nor a failed request may reach the empty state.
           *
           * "Alerts are going nowhere" on an instance with four working
           * channels is the most alarming wrong thing this page can say: it
           * tells a self-hoster their alerting is gone when in fact one
           * request 500'd. The error is stated above and this space stays
           * quiet rather than filling it with a claim we cannot support.
           */
          <p className="add-help">
            {loading ? "Loading channels…" : "The list could not be loaded."}
          </p>
        ) : channels.length === 0 ? (
          /*
           * The empty state is a fact at headline weight, not an apology.
           *
           * No channels is not "nothing here yet": it is a silent
           * misconfiguration that looks exactly like a working install, and
           * every failure this product detects goes nowhere. It is not red —
           * red means something is failing right now, and nothing is — the
           * weight and the wording carry it (DESIGN.md §2.3).
           */
          <div className="nt-empty">
            <p className="add-title">Alerts are going nowhere.</p>
            <p className="add-help">
              SubGlance will record every failure it detects and show it on the
              dashboard, and nobody will be told about any of them. Adding one
              channel and testing it takes about a minute.
            </p>
            {onCreateOpenChange !== undefined && (
              <div className="add-actions">
                <button
                  type="button"
                  className="add-button add-button-primary"
                  onClick={() => onCreateOpenChange(true)}
                >
                  Add a channel
                </button>
              </div>
            )}
          </div>
        ) : (
          <ul className="inv-list">
            {channels.map((channel) => (
              <ChannelRow
                key={channel.id}
                channel={channel}
                delivery={deliveries[channel.id] ?? DELIVERY_UNKNOWN}
                testing={testingIds.has(channel.id)}
                onTest={onTest}
                onEdit={canWrite ? setEditingId : undefined}
                onDelete={onDelete === undefined ? undefined : setConfirming}
                rowError={rowErrors[channel.id] ?? null}
              />
            ))}
          </ul>
        )}
      </Card>

      {/*
       * Add, in a drawer over the list rather than on its own page.
       *
       * You add a channel *against* the list — to check the name is not
       * already taken, that you are not adding a fourth Slack webhook nobody
       * can tell apart — and a full page navigation throws that away for a
       * task meant to take a minute.
       */}
      <Drawer
        open={createOpen && canWrite}
        onClose={() => onCreateOpenChange?.(false)}
        title="Add channel"
      >
        {onSave !== undefined && (
          <ChannelForm
            onSave={async (input) => {
              await onSave(null, input);
              onCreateOpenChange?.(false);
            }}
            onCancel={() => onCreateOpenChange?.(false)}
          />
        )}
      </Drawer>

      <Drawer
        open={editing !== null}
        onClose={() => setEditingId(null)}
        title={editing === null ? "Edit channel" : `Edit ${editing.name}`}
      >
        {editing !== null && onSave !== undefined && (
          <ChannelForm
            /* Keyed on id and name, so re-opening a channel that changed
               elsewhere rebuilds the form from the new values rather than
               keeping state from the previous open. */
            key={`${editing.id}:${editing.name}`}
            channel={editing}
            onSave={async (input) => {
              await onSave(editing.id, input);
              setEditingId(null);
            }}
            onCancel={() => setEditingId(null)}
          />
        )}
      </Drawer>

      {/*
       * Deleting names what will be destroyed and what goes silent with it.
       *
       * Not a `confirm()` and not one click: deleting a channel detaches it
       * from every monitor pointing at it, and those monitors then alert
       * nobody — a consequence that is not obvious from a button saying
       * Delete.
       */}
      <Drawer
        open={deleteTarget !== null}
        onClose={() => setConfirming(null)}
        title="Delete channel"
        footer={
          deleteTarget === null ? null : (
            <>
              <button
                type="button"
                className="add-button"
                onClick={() => setConfirming(null)}
              >
                Keep it
              </button>
              <button
                type="button"
                className="add-button inv-act--danger"
                onClick={() => {
                  onDelete?.(deleteTarget.id);
                  setConfirming(null);
                }}
              >
                Delete {deleteTarget.name}
              </button>
            </>
          )
        }
      >
        {deleteTarget !== null && (
          <p className="add-help">
            <b>{deleteTarget.name}</b> is removed, along with its stored
            credentials. Any monitor that alerts only through it will go on
            being checked and will tell nobody when it breaks. This cannot be
            undone.
          </p>
        )}
      </Drawer>

      {!canWrite && !loading && channels.length > 0 && (
        <p className="add-help">
          <StateChip>read only</StateChip> This account may see the channels
          but not change them, and may not send a test — a test is a real
          message to somebody else's inbox.
        </p>
      )}

      {/*
       * What is deliberately not on this page.
       *
       * Stated rather than silently missing, because the approved mockup draws
       * all of it and a reader comparing the two would otherwise conclude the
       * page is unfinished by accident.
       */}
      <p className="add-help">
        Routing rules, quiet hours, severity floors and the delivery log are
        not here. They have no backend today (SUB-124), and a quiet-hours
        switch that silently changes nothing is worse than no switch at all.
        Which monitors use a channel is set on the monitor.
      </p>
    </section>
  );
}
