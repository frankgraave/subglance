import { useMemo, useState } from "react";
import { Card, Panel } from "../components/Card";
import { PlusIcon, SearchIcon } from "../shell/icons";
import { ToolbarTools, TopbarTools } from "../shell/TopbarTools";
import { Drawer } from "../components/Drawer";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { StateChip } from "../components/Chip";
import { EmptyState } from "./EmptyState";
import { MonitorInventoryRow } from "./MonitorInventoryRow";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { filterMonitors } from "./model";
import { describeInventory, filterByType } from "./inventory";
import type { ChannelState, InventoryMonitor } from "./inventory";
import type { CheckOutcome, MonitorPatch } from "./inventoryApi";

/**
 * The inventory: what is configured, and where it is changed.
 *
 * **Why this is not the dashboard.** The dashboard is for watching — heartbeat,
 * latency, 24h uptime — and is meant to be left open on a second screen. This
 * page is for managing, so it shows the columns the dashboard refuses (type,
 * interval, timeout, channels, tags) and drops the ones the dashboard owns (no
 * beat bar, no latency, no uptime). The dividing line the design states: if you
 * can answer it by looking it belongs on the dashboard; if you answer it by
 * editing it belongs here.
 *
 * **It shows paused monitors by default, where the dashboard hides them.** That
 * difference is the strongest argument for a separate page rather than a fifth
 * dashboard layout: the two views want opposite defaults, and a settings mode
 * inside a screen people leave open on a wall display invites accidents.
 *
 * **There is no bulk mode.** See the PR for the full argument; the short
 * version is that there is no bulk endpoint, so bulk pause is N requests, and a
 * bulk action that half-succeeds while reporting success is worse than no bulk
 * at all. Per-row actions are unambiguous: each one either worked or says why
 * it did not, on the row it belongs to.
 *
 * Presentational, like `Dashboard` and `IncidentsView`: it fetches nothing, so
 * a test renders it from a fixture. `LiveMonitors` above it owns the data.
 */

export type MonitorsViewProps = {
  monitors: readonly InventoryMonitor[];
  /** Per monitor id; a missing id renders as "not loaded", never as "none". */
  channels?: Readonly<Record<string, ChannelState>>;
  loading?: boolean;
  error?: Error | null;
  /** Opens a monitor's detail view. */
  onOpen?: (id: string) => void;
  /** Pauses or resumes. Absent for a viewer, who may not write. */
  onTogglePaused?: (id: string, paused: boolean) => void;
  onCheckNow?: (id: string) => void;
  onDelete?: (id: string) => void;
  /** Applies an edit. Resolves when the server accepted it. */
  onSave?: (id: string, patch: MonitorPatch) => Promise<void>;
  /**
   * Asks the owner to load a monitor for editing. Absent for a viewer.
   *
   * The owner re-reads it rather than this screen handing over the list row,
   * because the edit has to be filled from the same response the ETag came
   * with — a precondition guarding a different instant from the values on
   * screen is worse than none, since it looks like it worked.
   */
  onEdit?: (id: string) => void;
  /** The monitor the owner loaded, freshly read. Null closes the drawer. */
  editing?: InventoryMonitor | null;
  /** A failed load of the monitor to edit. */
  editError?: string | null;
  onEditClose?: () => void;
  /** Called after a monitor is created, so the owner can refetch. */
  onCreated?: () => void;
  /** Ids with a pause/resume in flight. */
  busyIds?: ReadonlySet<string>;
  /** Ids with a manual check in flight. */
  checkingIds?: ReadonlySet<string>;
  /** The last manual check per monitor id. */
  checkResults?: Readonly<Record<string, CheckOutcome>>;
  /** A failed write per monitor id, in the server's own words. */
  rowErrors?: Readonly<Record<string, string>>;
  /** True when the URL asked for the create form: /monitors/new. */
  createOpen?: boolean;
  /** Opens or closes the create drawer, and moves the URL with it. */
  onCreateOpenChange?: (open: boolean) => void;
};

const NO_SET: ReadonlySet<string> = new Set();
const NO_MAP = {};

export function MonitorsView({
  monitors,
  channels = NO_MAP,
  loading = false,
  error = null,
  onOpen,
  onTogglePaused,
  onCheckNow,
  onDelete,
  onSave,
  onEdit,
  editing = null,
  editError = null,
  onEditClose,
  onCreated,
  busyIds = NO_SET,
  checkingIds = NO_SET,
  checkResults = NO_MAP,
  rowErrors = NO_MAP,
  createOpen = false,
  onCreateOpenChange,
}: MonitorsViewProps) {
  const [query, setQuery] = useState("");
  const [type, setType] = useState<string>("");
  /** "" = every monitor, "active", "paused". The default shows everything,
   *  which is precisely where this page differs from the dashboard. */
  const [pausedFilter, setPausedFilter] = useState<string>("");
  const [confirming, setConfirming] = useState<string | null>(null);

  const visible = useMemo(() => {
    let out = filterByType(filterMonitors(monitors, query), type);
    if (pausedFilter === "active") out = out.filter((m) => m.enabled);
    if (pausedFilter === "paused") out = out.filter((m) => !m.enabled);
    return out;
  }, [monitors, query, type, pausedFilter]);

  const deleteTarget = monitors.find((m) => m.id === confirming) ?? null;

  return (
    <section className="mon-detail inv-screen" aria-label="Monitors">
      {/*
       * No page heading (SUB-138).
       *
       * The sidebar already says Monitors, and the card below says
       * "Configured monitors" — three statements of the same word before a
       * single row of data. The dashboard never had one, which is what made
       * the inconsistency visible: the two list screens looked like different
       * products. The card's title is the heading, and the count that used to
       * sit under the page title is the toolbar's "n of m shown".
       *
       * `aria-label` on the section still names the region for assistive
       * technology, so removing the visible heading does not remove the
       * landmark's name.
       */}

      {error !== null && (
        <p className="inc-notice" role="alert">
          {error.message}
        </p>
      )}

      {/* A monitor that could not be re-read for editing. Stated rather than
          silently doing nothing: a button that opens no drawer reads as a
          broken page. */}
      {editError !== null && (
        <p className="inc-notice" role="alert">
          {editError}
        </p>
      )}

      {/*
       * Search in the masthead, filters in the page toolbar (SUB-138).
       *
       * Both used to sit together in one bar, which made the monitors page
       * look different from the dashboard even though both screens search a
       * list of monitors. The split follows what the control *is*: searching
       * "the things this screen lists" is true everywhere and holds the same
       * position everywhere, while a type facet is this screen's alone.
       *
       * The counter travels with the filters, and that is not tidiness.
       * "3 of 3 shown" is the filter's honesty — it says you are looking at a
       * selection rather than at everything — so it belongs beside the
       * controls that make the claim true.
       *
       * Portalled rather than passed up as props, so the filter state stays
       * inside the screen that filters. See `TopbarTools`.
       */}
      <TopbarTools>
        <label className="shell-search">
          <span className="sr-only">Search monitors</span>
          <SearchIcon />
          <input
            type="search"
            /* `shell-search-input` pins the 16px minimum at every width.
               `.add-input` drops to 14px above 640px, which is fine for a
               form nobody types into on a phone in landscape and wrong for a
               search box: iOS Safari zooms the page on focus below 16px and
               leaves the reader scrolled sideways (DESIGN.md §13). */
            className="shell-search-input"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search monitors…"
          />
        </label>
      </TopbarTools>

      <ToolbarTools>
        <div className="tb-group">
          <label className="tb-field">
            <span className="tb-label">Type</span>
            <select
              className="tb-select"
              value={type}
              onChange={(event) => setType(event.target.value)}
            >
              <option value="">All types</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="ssl">SSL</option>
              <option value="push">Push</option>
            </select>
          </label>

          <label className="tb-field">
            <span className="tb-label">Paused</span>
            <select
              className="tb-select"
              value={pausedFilter}
              onChange={(event) => setPausedFilter(event.target.value)}
            >
              {/* "All" first and selected by default. The dashboard hides
                  paused monitors; this page must show them, because a monitor
                  someone paused during a deploy and forgot is exactly the
                  thing an inventory is read to find. */}
              <option value="">All</option>
              <option value="active">Active only</option>
              <option value="paused">Paused only</option>
            </select>
          </label>

          <p className="tb-count" role="status">
            {loading
              ? "Loading monitors…"
              : `${visible.length} of ${monitors.length} shown`}
          </p>
        </div>
      </ToolbarTools>

      <Card
        title="Configured monitors"
        headingLevel={1}
        /*
         * `h1`, because this card's title is now the page's heading (SUB-138).
         * The document must still have exactly one, and the level is a fact
         * about where the card sits rather than a style choice.
         */
        note={describeInventory(monitors)}
        action={
          onCreateOpenChange === undefined ? undefined : (
            <button
              type="button"
              className="add-button add-button-primary"
              /*
               * Named explicitly rather than left to its text content. The
               * button is a glyph plus a word, and what a screen reader makes
               * of an inline <svg> is not fixed — an unnamed one is skipped by
               * some and announced as "graphic" by others, which would make
               * this button's name depend on the reader rather than on us.
               * The visual order is set in CSS; the name is set here.
               */
              aria-label="Add monitor"
              onClick={() => onCreateOpenChange(true)}
            >
              {/* The glyph accompanies the words rather than replacing them:
                  this is the one action on the screen that should be findable
                  without reading, and a `+` alone would make it the only
                  unlabelled primary button in the product. */}
              <PlusIcon aria-hidden="true" />
              Add monitor
            </button>
          )
        }
      >
        {loading || error !== null ? (
          /*
           * Neither loading nor a failed request may reach the empty state.
           *
           * "Nothing is being watched yet" on an instance with forty monitors
           * is the single most alarming wrong thing this screen can say: it
           * tells a self-hoster their configuration is gone when in fact one
           * request 500'd. The error is stated above, and this space stays
           * quiet rather than filling it with a claim we cannot support.
           */
          <p className="add-help">
            {loading ? "Loading monitors…" : "The list could not be loaded."}
          </p>
        ) : monitors.length === 0 ? (
          <EmptyState
            query={query}
            totalCount={0}
            onAddMonitor={
              onCreateOpenChange === undefined
                ? undefined
                : () => onCreateOpenChange(true)
            }
          />
        ) : visible.length === 0 ? (
          <EmptyState
            query={query}
            totalCount={monitors.length}
            filtered={type !== "" || pausedFilter !== ""}
          />
        ) : (
          <ul className="inv-list">
            {visible.map((monitor) => (
              <MonitorInventoryRow
                key={monitor.id}
                monitor={monitor}
                channels={channels[monitor.id] ?? { known: false }}
                onOpen={onOpen}
                onTogglePaused={onTogglePaused}
                onCheckNow={onCheckNow}
                onEdit={onEdit}
                onDelete={onDelete === undefined ? undefined : setConfirming}
                busy={busyIds.has(monitor.id)}
                checking={checkingIds.has(monitor.id)}
                checkResult={checkResults[monitor.id] ?? null}
                rowError={rowErrors[monitor.id] ?? null}
              />
            ))}
          </ul>
        )}
      </Card>

      {/*
       * Create, in a drawer over the inventory rather than on its own page.
       *
       * You add a monitor *from* the list and check it *against* the list —
       * that the name is still free, that the interval matches its neighbours
       * — and a full page navigation throws that context away for a task that
       * is meant to take a minute. It is modal, so it owns the screen while
       * open and the two-surface budget is spent on it rather than beside the
       * list, and `/monitors/new` is a real address that opens it.
       */}
      <Drawer
        open={createOpen}
        onClose={() => onCreateOpenChange?.(false)}
        title="Add monitor"
      >
        {/* Card holding a Panel, the same as everywhere else in the product
            (SUB-132). A drawer is a place to put the existing surfaces, not a
            second visual language, and the form on its own is bare fields on
            the drawer's own background. */}
        {/*
         * No Card around the form (SUB-138).
         *
         * The drawer is already a surface with a header; wrapping the form in a
         * card made three nested frames — drawer, card, then the form's own
         * fieldset — and PR #42 fixed the rule at two surfaces, never three. The
         * drawer's header is the card header this was reaching for.
         */}
        <Panel>
          <AddMonitor
            onCreated={() => {
              onCreated?.();
              onCreateOpenChange?.(false);
            }}
            onCancel={() => onCreateOpenChange?.(false)}
          />
        </Panel>
      </Drawer>

      <Drawer
        open={editing !== null}
        onClose={() => onEditClose?.()}
        title={editing === null ? "Edit monitor" : `Edit ${editing.name}`}
      >
        {editing !== null && onSave !== undefined && (
          <Card title={editing.name} headingLevel={3}>
            <Panel>
              <EditMonitorForm
              /* Keyed on the id AND the name, so re-opening a monitor that
                 changed elsewhere rebuilds the form from the new values
                 rather than keeping state from the previous open. */
                key={`${editing.id}:${editing.name}`}
                monitor={editing}
                onSave={(patch) => onSave(editing.id, patch)}
                onReload={() => onEdit?.(editing.id)}
                onCancel={() => onEditClose?.()}
              />
            </Panel>
          </Card>
        )}
      </Drawer>

      {/*
       * Deleting names what will be destroyed, and what goes with it.
       *
       * Not a `confirm()` and not a one-click action: deleting a monitor takes
       * its heartbeats, its uptime history and its incident record with it, and
       * that consequence is not obvious from a button that says Delete.
       *
       * The retyping is DESIGN.md §7.5, and it was missing here too — this
       * screen shipped with the one-click version, which is why the rule now
       * lives in `ConfirmDelete` rather than being written out per screen.
       */}
      {deleteTarget !== null && (
        <ConfirmDelete
          open
          onClose={() => setConfirming(null)}
          kind="monitor"
          name={deleteTarget.name}
          consequence={`${deleteTarget.name} and everything recorded about it — heartbeats, uptime history and past incidents — are removed. This cannot be undone. If you only want it to stop checking, pause it instead: a paused monitor keeps its history.`}
          onConfirm={() => {
            onDelete?.(deleteTarget.id);
            setConfirming(null);
          }}
        />
      )}

      {onTogglePaused === undefined && !loading && monitors.length > 0 && (
        <p className="add-help">
          <StateChip>read only</StateChip> This account may read the inventory
          but not change it.
        </p>
      )}
    </section>
  );
}
