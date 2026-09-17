import { useState } from "react";
import { Card } from "../components/Card";
import { PlusIcon, SearchIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { Drawer } from "../components/Drawer";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { StateChip } from "../components/Chip";
import { ChannelRow } from "./ChannelRow";
import { ChannelForm } from "./ChannelForm";
import { describeChannels } from "./channels";
import type { Channel, DeliveryState } from "./channels";
import { DELIVERY_UNKNOWN } from "./channels";
import type { ChannelInput } from "./channelsApi";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_TOGGLING: ReadonlySet<string> = new Set();

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
  /** Turns delivery through a channel on or off. Absent for a viewer. */
  onSetEnabled?: (id: string, enabled: boolean) => void;
  /** Channels whose enable/disable write is in flight. */
  togglingIds?: ReadonlySet<string>;
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
  onSetEnabled,
  togglingIds = EMPTY_TOGGLING,
  onSave,
  createOpen = false,
  onCreateOpenChange,
}: NotificationsViewProps) {
  const [query, setQuery] = useState("");
  /*
   * Filtering by what a row shows: its name and its type. Not by the secret,
   * obviously, and not by the destination either — the destination is masked
   * for most channel types, so a query would appear to search something the
   * page will not show you.
   */
  const needle = query.trim().toLowerCase();
  const visibleChannels =
    needle === ""
      ? channels
      : channels.filter(
          (channel) =>
            channel.name.toLowerCase().includes(needle) ||
            channel.type.toLowerCase().includes(needle),
        );
  const [editingId, setEditingId] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const editing = channels.find((c) => c.id === editingId) ?? null;
  const deleteTarget = channels.find((c) => c.id === confirming) ?? null;
  const canWrite = onSave !== undefined;

  return (
    <section className="mon-detail inv-screen" aria-label="Notifications">
      {/*
       * Search in the masthead, like every other list screen (SUB-138). It
       * filters by channel name and by type, which are the two things written
       * on a row.
       */}
      <TopbarTools>
        <label className="shell-search">
          <span className="sr-only">Filter channels by name or type</span>
          <SearchIcon />
          <input
            type="search"
            className="shell-search-input"
            value={query}
            placeholder="Filter channels…"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
      </TopbarTools>

      {/* No visible page heading: the sidebar says Notifications and the card
          below says Channels. The `h1` stays for heading navigation. */}
      <h1 className="sr-only">Notifications</h1>

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
        /* This card's title is the page's heading now (SUB-138). */
        headingLevel={1}
        note={describeChannels(channels)}
        action={
          onCreateOpenChange === undefined ? undefined : (
            <button
              type="button"
              className="add-button add-button-primary"
              // Named here rather than by its text content, for the same
              // reason as Add monitor: an unnamed inline <svg> leaves the
              // button's accessible name up to the screen reader.
              aria-label="Add channel"
              onClick={() => onCreateOpenChange(true)}
            >
              <PlusIcon aria-hidden="true" />
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
        ) : visibleChannels.length === 0 ? (
          /*
           * A filter that matched nothing, which is not the same fact.
           *
           * "Alerts are going nowhere" is a claim about the instance. Typed
           * over a search, it told a self-hoster their alerting was gone when
           * four working channels were sitting one keystroke away. The filter
           * gets its own line, and it does not repeat the alarm.
           */
          <p className="add-help">
            No channels match “{query.trim()}”. The filter matches a channel’s
            name and its type.
          </p>
        ) : (
          <ul className="inv-list">
            {visibleChannels.map((channel) => (
              <ChannelRow
                key={channel.id}
                channel={channel}
                delivery={deliveries[channel.id] ?? DELIVERY_UNKNOWN}
                testing={testingIds.has(channel.id)}
                onTest={onTest}
                onEdit={canWrite ? setEditingId : undefined}
                onDelete={onDelete === undefined ? undefined : setConfirming}
                onSetEnabled={onSetEnabled}
                toggling={togglingIds.has(channel.id)}
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
       * Delete. DESIGN.md §7.5 asks for the name to be retyped, and
       * `ConfirmDelete` is where that rule now lives so the next destructive
       * action inherits it rather than rediscovering it.
       */}
      {deleteTarget !== null && (
        <ConfirmDelete
          open
          onClose={() => setConfirming(null)}
          kind="notification channel"
          name={deleteTarget.name}
          consequence={`${deleteTarget.name} is removed, along with its stored credentials. Any monitor that alerts only through it will go on being checked and will tell nobody when it breaks. This cannot be undone.`}
          onConfirm={() => {
            onDelete?.(deleteTarget.id);
            setConfirming(null);
          }}
        />
      )}

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
