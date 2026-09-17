import { useState } from "react";
import { Card } from "../components/Card";
import { PlusIcon, SearchIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { Drawer } from "../components/Drawer";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { StateChip } from "../components/Chip";
import { ChannelRow } from "./ChannelRow";
import { ChannelForm } from "./ChannelForm";
import { typeLabel, CHANNEL_TYPES } from "./channels";
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

      <Card
        className="nt-card"
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
         * `h2`, under the screen-reader-only `Notifications` `h1` above.
         *
         * `Card` renders `headingLevel` as a real heading element, so
         * `headingLevel={1}` put a second `h1` in the document: the page's own
         * sr-only one naming the route, and this card's naming a section
         * inside it. Two level-one headings is not an outline, and the reader
         * it costs is exactly the one the sr-only heading exists for — heading
         * navigation stops distinguishing "the page" from "a card on it"
         * (CodeRabbit, PR #61).
         *
         * `IncidentsView` already has this shape — an sr-only `h1` for the
         * route and its cards at `h2` — and this screen is the outlier.
         * `MonitorsView` keeps `headingLevel={1}` correctly, because it has no
         * sr-only heading and its card title is the page's only one.
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
              <p className="add-title">Alerts are going nowhere.</p>
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
                <div className="add-actions">
                  <button
                    type="button"
                    className="add-button add-button-primary"
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
              {channels.length === 1 ? "channel is" : "channels are"} configured.
            </p>
          )
        ) : (
          <>
            {/*
             * The caveat that explains why no row says "delivered", as one
             * line that opens into the whole of it (SUB-138).
             *
             * It was rejected as a six-line block of prose sitting above two
             * rows on a real instance — physically larger than the list it
             * qualifies, so you had to read an explanation of the Delivery
             * column before you ever reached the Delivery column. The previous
             * pass had been asked to make it *more legible* and made it bigger
             * and earlier instead, which is not the same thing.
             *
             * Nothing is cut. Every clause is in the DOM, in the same words,
             * and the summary is not a teaser — it states the fact itself, so
             * a reader who never opens the disclosure has still been told the
             * thing they most need to know. What the disclosure removes is
             * six lines of weight above a two-line list, not the content: the
             * detail is the *reason*, which is worth reading once and worth
             * nobody's eye a second time.
             *
             * Open by default would be the same block again. Closed by
             * default, above the list, and the one sentence is short enough to
             * be read on the way past.
             *
             * The summary states the absence of history rather than claiming
             * that nothing below works. It used to say "No channel below is
             * known to be working", and a row that has just passed a test says
             * "Test delivered" and that the channel can deliver right now — so
             * the two contradicted each other on the same screen the moment
             * anyone pressed Send test. The caveat SUB-55 requires is the
             * missing history, and that is what survives here; the row result
             * is a separate, narrower claim about one moment and is left to
             * make it.
             */}
            <details className="nt-legend">
              <summary className="nt-legend-summary">
                SubGlance keeps no delivery history — no record of any alert
                arriving.
              </summary>
              <p className="nt-legend-body">
                The channel API carries no delivery history, so a channel that
                has failed every delivery for three days looks exactly the same
                here as one that has never been needed. Sending a test is the
                only thing that tells you which you have, and it proves only
                that the channel worked at the moment you pressed it.
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
        <p className="nt-note">
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
       *
       * Given a heading rather than left as an unlabelled grey paragraph
       * trailing the card (SUB-138). Unheaded caveat prose at the bottom of a
       * screen reads as boilerplate and is skipped; what it actually contains
       * is a scope decision a reader may want to disagree with, so it is
       * framed as one. The text is unchanged and the measure is bounded, which
       * is the only reason it is now legible at all.
       */}
      <aside className="nt-scope" aria-label="Not on this page">
        <p className="nt-scope-legend">Not on this page</p>
        <p className="nt-note">
          Routing rules, quiet hours, severity floors and the delivery log are
          not here. They have no backend today (SUB-124), and a quiet-hours
          switch that silently changes nothing is worse than no switch at all.
          Which monitors use a channel is set on the monitor.
        </p>
      </aside>
    </section>
  );
}