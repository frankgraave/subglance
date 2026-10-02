import { useState } from "react";
import { Card } from "../components/Card";
import { IconSend } from "../components/icons";
import { PlusIcon, SearchIcon } from "../shell/icons";
import { ToolbarTools } from "../shell/ToolbarTools";
import { Drawer } from "../components/Drawer";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { StateChip } from "../components/Chip";
import { ChannelRow } from "./ChannelRow";
import { ChannelForm } from "./ChannelForm";
import { CoverageCard } from "./CoverageCard";
import type { InventoryMonitor } from "../monitors/inventory";
import { typeLabel, CHANNEL_TYPES } from "./channels";
import type { Channel, DeliveryState } from "./channels";
import { DELIVERY_UNKNOWN, HISTORY_UNKNOWN } from "./channels";
import type { ChannelInput } from "./channelsApi";
import type { QuietHours } from "./channels";

/** Shared empty default: a new Set per render would break memoisation. */
const EMPTY_TOGGLING: ReadonlySet<string> = new Set();

/**
 * Notifications: can SubGlance reach you at all, and who hears about what.
 *
 * The page answers which destinations exist, whether they work, how to change
 * them, and — since SUB-124 — where a monitor with no channels of its own
 * sends its alerts, and when each channel stays quiet. The approved mockup
 * also draws severity floors and a delivery log. Those have no backend yet,
 * and a switch that
 * looks like it suppresses alerts at 03:00 while changing nothing is the most
 * dangerous shape of dead UI this product could ship. So they are absent
 * rather than disabled: a disabled control is a feature that looks
 * temporarily unavailable, and these do not exist.
 *
 * Presentational, like `MonitorsView` and `IncidentsView`: it fetches nothing,
 * so a test drives every state from a fixture. `LiveNotifications` owns the
 * data.
 */

/**
 * The window the Delivery column covers, as the server's own records state
 * it. A server that sent none falls back to its retention default, which is
 * what every server that does send one also says.
 */
function historyWindowDays(channels: readonly Channel[]): number {
  return (
    channels.find((channel) => channel.history.state !== "unknown")?.history
      .windowDays ?? HISTORY_UNKNOWN.windowDays
  );
}

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
  /**
   * Moves the instance default to a channel, or clears it with null. Absent
   * for a viewer, who is shown the default as a sentence instead.
   */
  onSetDefault?: (id: string | null) => void;
  /** True while a default change is in flight. */
  savingDefault?: boolean;
  /** A refused default change, in the server's own words. */
  defaultError?: string | null;
  /**
   * Saves a new or edited channel, and its quiet hours when `quiet` is not
   * undefined. Resolves when the server accepted it.
   */
  onSave?: (
    id: string | null,
    input: ChannelInput,
    quiet?: QuietHours | null,
  ) => Promise<void>;
  /** True when the URL asked for the create form: /notifications/new. */
  createOpen?: boolean;
  onCreateOpenChange?: (open: boolean) => void;
  /**
   * The monitor list, for the "who hears what" card. Undefined leaves the
   * card out entirely; null means it has not arrived yet.
   */
  monitors?: readonly InventoryMonitor[] | null;
  monitorsFailed?: boolean;
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
  onSetDefault,
  savingDefault = false,
  defaultError = null,
  onSave,
  createOpen = false,
  onCreateOpenChange,
  monitors,
  monitorsFailed = false,
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
  const defaultChannel = channels.find((c) => c.isDefault) ?? null;
  const canWrite = onSave !== undefined;

  return (
    <section className="mon-detail inv-screen" aria-label="Notifications">
      {/*
       * The filter field, in this page's toolbar like every other list
       * screen's (SUB-182). It filters by channel name and by type, which are
       * the two things written on a row. It is the toolbar's only control, so
       * the bar appears here for one field: the alternative, a field in the
       * masthead, was a second search entry beside the command menu.
       */}
      <ToolbarTools>
        <div className="tb-group">
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
        </div>
      </ToolbarTools>

      {/* The page frame's visible `h1` names this screen (SUB-182). */}

      {error !== null && (
        <p className="inc-notice" role="alert">
          {error.message}
        </p>
      )}

      <Card
        className="nt-card"
        icon={<IconSend />}
        /*
         * `Channels (2)`, not "Channels" over "2 channels, 1 disabled".
         *
         * The count belongs in the heading rather than on a line of its own,
         * and it is the form the Monitors screen uses for the same fact, so
         * the two screens can be read the same way. A two-line header also
         * made the card's header taller than the rule that separates it from
         * the list, which is what made it look misaligned against its own
         * edges.
         *
         * The parenthesis appears only once the count is actually known. It
         * used to be `describeChannels(channels)` unconditionally, and
         * `channels` is `[]` before the first response — so the card headed
         * itself "No channels configured" while its own body said "Loading
         * channels…", in the same screenshot. Two sentences, one screen,
         * flatly contradicting each other, and the wrong one was the
         * confident one. `Channels (0)` over "Loading channels…" would be
         * that same lie in fewer characters, so the heading stays bare until
         * a response has arrived.
         *
         * Bare on a genuinely empty instance too, and that is the
         * three-headings fix. The empty state used to stack "Channels" (card
         * title), "No channels configured" (a note) and "Alerts are going
         * nowhere." (the body's headline) — three names for one thing, which
         * is exactly the pattern PR #57 removed from the add drawer with "two
         * surfaces, never three". The body's headline is the one that says
         * something, so it is the one that stays; a count of zero above it is
         * the same fact said worse, first.
         *
         * The disabled count is not lost with the note: it is on the rows, as
         * a `Disabled` chip beside the name of each channel that is switched
         * off. That is strictly more than the header line carried — the chip
         * names *which* channel is silent, where "1 disabled" only told you
         * that one of them was and left you to find it — and it is the only
         * one of the two that stays true under the masthead filter, which
         * hides rows without changing `channels.length`.
         */
        title={
          loading || error !== null || channels.length === 0
            ? "Channels"
            : `Channels (${channels.length})`
        }
        /*
         * `h2`, under the page frame's `Notifications` `h1`. Every screen's
         * cards are `h2` now (SUB-182): one `h1` per document, and it is the
         * page's, so heading navigation can tell "the page" from "a card on
         * it" (CodeRabbit, PR #61).
         */
        headingLevel={2}
        /*
         * The header action steps aside for the empty state (SUB-138).
         *
         * With no channels the screen offered "Add channel" in the card header
         * and "Add a channel" in the body — two primary buttons, 200px apart,
         * doing the same thing, on the one screen whose job is to present a
         * single obvious next step. The empty state's button is the one that
         * keeps its explanation beside it, so it is the one that stays.
         *
         * Only for the genuinely empty instance: while loading, on an error,
         * and on a list filtered down to nothing, the header button is the
         * only way to add a channel and must not vanish.
         */
        action={
          onCreateOpenChange === undefined ||
          (!loading && error === null && channels.length === 0) ? undefined : (
            <button
              type="button"
              className="button button--primary"
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
          <p className="nt-note">
            {loading ? "Loading channels…" : "The list could not be loaded."}
          </p>
        ) : visibleChannels.length === 0 ? (
          channels.length === 0 ? (
            /*
             * The empty state is a fact at headline weight, not an apology.
             *
             * No channels is not "nothing here yet": it is a silent
             * misconfiguration that looks exactly like a working install, and
             * every failure this product detects goes nowhere. It is not red —
             * red means something is failing right now, and nothing is — the
             * weight and the wording carry it (DESIGN.md §2.3).
             *
             * It also says what a channel *is* (SUB-138). This is the first
             * screen a new self-hoster reaches with nothing configured, and
             * the previous version assumed the reader already knew what they
             * were being asked to add: it named a consequence and offered a
             * button, with nothing in between. The types are listed because
             * they are the honest answer to "what can I even use here" — they
             * come from `CHANNEL_TYPES`, which mirrors the store's CHECK
             * constraint, so the list cannot drift into advertising a type the
             * server would reject.
             */
            <div className="nt-empty">
              <p className="form-title">Alerts are going nowhere.</p>
              <p className="nt-note nt-note--lede">
                A channel is where SubGlance sends a message when a monitor
                fails. Until one exists, every failure is still detected,
                recorded and drawn on the dashboard — and nobody is ever told.
              </p>
              <p className="nt-note">
                {CHANNEL_TYPES.map(typeLabel).join(", ")} are supported. Adding
                one and testing it takes about a minute.
              </p>
              {onCreateOpenChange !== undefined && (
                <div className="button-row">
                  <button
                    type="button"
                    className="button button--primary"
                    onClick={() => onCreateOpenChange(true)}
                  >
                    <PlusIcon aria-hidden="true" />
                    Add a channel
                  </button>
                </div>
              )}
            </div>
          ) : (
            /*
             * A filter that matched nothing is not an empty instance.
             *
             * Previously the two shared one branch, so typing "zz" into the
             * masthead filter on an instance with four working channels
             * produced "Alerts are going nowhere." — the page's single most
             * alarming sentence, fired by a search box. The distinction costs
             * one comparison and prevents the screen from lying about the
             * thing it exists to be honest about.
             */
            <p className="nt-note">
              No channel matches {`“${query.trim()}”`}. {channels.length}{" "}
              {channels.length === 1 ? "channel is" : "channels are"}{" "}
              configured.
            </p>
          )
        ) : (
          <>
            {/*
             * Where an unrouted monitor's alerts go (SUB-124), as one
             * sentence above the list rather than a sixth control on every
             * row.
             *
             * It is a fact about the instance, not about a channel: exactly
             * one channel or none can hold it, and a per-row toggle would
             * make the reader scan every row to find out which. As a
             * sentence the answer is read once, and with no default it says
             * plainly that those monitors reach nobody — the failure the
             * default exists to prevent, so it is not softened.
             *
             * A native select, because the choice is one of N named things
             * plus "nobody", which is exactly what a select is, and it brings
             * keyboard and screen-reader behaviour nobody has to rebuild.
             */}
            <div className="nt-note nt-default">
              {onSetDefault === undefined ? (
                defaultChannel === null ? (
                  <p>
                    Monitors with no channels of their own alert nobody: no
                    default channel is set.
                  </p>
                ) : (
                  <p>
                    Monitors with no channels of their own alert through{" "}
                    <strong>{defaultChannel.name}</strong>
                    {/* The notifier skips a disabled channel, so a disabled
                        default is configured but delivers nothing. */}
                    {defaultChannel.enabled
                      ? "."
                      : ", which is disabled: they alert nobody until it is enabled."}
                  </p>
                )
              ) : (
                <label>
                  Monitors with no channels of their own alert through{" "}
                  <select
                    className="input input--fit"
                    value={defaultChannel?.id ?? ""}
                    disabled={savingDefault}
                    aria-busy={savingDefault}
                    onChange={(event) =>
                      onSetDefault(
                        event.target.value === "" ? null : event.target.value,
                      )
                    }
                  >
                    <option value="">nobody (no default)</option>
                    {channels.map((channel) => (
                      <option key={channel.id} value={channel.id}>
                        {channel.enabled
                          ? channel.name
                          : `${channel.name} (disabled)`}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {defaultError !== null && (
                <p className="inv-result inv-result--bad" role="alert">
                  {defaultError}
                </p>
              )}
            </div>

            {/*
             * What the Delivery column means, as one line that opens into the
             * whole of it (SUB-138, rewritten for SUB-180).
             *
             * A disclosure because the previous caveat was rejected as a
             * six-line block above a two-row list. The summary states the
             * fact rather than teasing it, so a reader who never opens it has
             * still been told what the column covers; the body is the reason,
             * worth reading once.
             *
             * It used to say "SubGlance keeps no delivery history", which was
             * true of the API and is not any more: the column below is read
             * from the outbox. The caveat that survives is the one that is
             * still true and still the dangerous one to forget — a channel
             * with nothing in its window has proved nothing, and only a test
             * shows it works before an outage does.
             */}
            <details className="nt-legend">
              <summary className="nt-legend-summary">
                Delivery shows how real alerts went in the last{" "}
                {historyWindowDays(channels)} days.
              </summary>
              <p className="nt-legend-body">
                Failed means the newest alert gave up after its retries;
                Retrying means one is queued after a failed attempt. A channel
                with no alerts in that time has proved nothing either way, so
                it says so rather than looking healthy. Sending a test is the
                only way to know a quiet channel works before an outage needs
                it, and it proves only that it worked at that moment.
              </p>
            </details>

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
          </>
        )}
      </Card>

      {/*
       * Who hears what, under the channels it is computed from. It waits for
       * the channel list too: without it a disabled channel cannot be told
       * from a live one, and the card would name routes that deliver nothing.
       */}
      {monitors !== undefined && error === null && (
        <CoverageCard
          monitors={monitors}
          channels={channels}
          loading={!monitorsFailed && (monitors === null || loading)}
          failed={monitorsFailed}
        />
      )}

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
            onSave={async (input, quiet) => {
              await onSave(null, input, quiet);
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
            onSave={async (input, quiet) => {
              await onSave(editing.id, input, quiet);
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
        <p className="nt-note">
          <StateChip>read only</StateChip> This account may see the channels but
          not change them, and may not send a test — a test is a real message to
          somebody else's inbox.
        </p>
      )}

      {/*
       * Where the rest of alerting is set, since it is not on this page.
       *
       * Given a heading rather than left as an unlabelled grey paragraph
       * trailing the card (SUB-138): unheaded caveat prose at the bottom of a
       * screen reads as boilerplate and is skipped.
       *
       * It used to say tag routing rules had no backend and point at a ticket
       * number. Both stopped being true or useful to anyone reading the page
       * (SUB-193): routing rules exist in the API and in configuration files,
       * and a ticket number means nothing to a person running the product. So
       * it says where each thing is set today. When the monitor form and this
       * page grow editors for them (SUB-179, SUB-158), this is the paragraph
       * that changes.
       */}
      <aside className="nt-scope" aria-label="Not on this page">
        <p className="nt-scope-legend">Not on this page</p>
        <p className="nt-note">
          Which monitors alert through which channels is set outside this page
          for now: a monitor&rsquo;s own channels and tag routing rules are set
          through the API or a configuration file imported under Settings,
          Import &amp; export. A monitor with neither alerts through the default
          channel. Quiet hours are set on each channel.
        </p>
      </aside>
    </section>
  );
}
