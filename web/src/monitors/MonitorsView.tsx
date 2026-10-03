import { useCallback, useEffect, useMemo, useState } from "react";
import { registerNavigationCleanup } from "../shell/leaveGuard";
import { Card, Panel } from "../components/Card";
import { PlusIcon, SearchIcon } from "../shell/icons";
import { IconClock, IconList, IconPause, IconPlay, IconPulse } from "../components/icons";
import { ToolbarTools } from "../shell/ToolbarTools";
import { Drawer } from "../components/Drawer";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { StateChip } from "../components/Chip";
import { Checkbox } from "../components/Choice";
import { EmptyState } from "./EmptyState";
import { MonitorInventoryHead, MonitorInventoryRow } from "./MonitorInventoryRow";
import { AddMonitor } from "./AddMonitor";
import { EditMonitorForm } from "./EditMonitorForm";
import { BulkTagDrawer, type TagChange } from "./BulkTagDrawer";
import { filterByTags, filterMonitors, liveTagSelection, sameTagSelection, tagFacets } from "./model";
import type { TagSelection } from "./model";
import { TagFilters } from "./TagFilters";
import { MaintenanceDrawer } from "./MaintenanceDrawer";
import {
  INVENTORY_SORTS,
  describeInventory,
  filterByType,
  monitorDeleteConsequence,
  sortInventory,
} from "./inventory";
import type { ChannelState, InventoryMonitor, InventorySort } from "./inventory";
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
 * Tag management uses a single atomic endpoint. Other changes stay per-row;
 * a series of independent writes must never pretend to be one bulk success.
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
  /** Atomic tag preview/commit. Absent for viewers. */
  onTagChange?: TagChange;
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
  /**
   * Whether this reader may schedule maintenance. A viewer still gets the
   * schedule, read-only: "is something already planned for tonight" is a
   * question anybody on the rota asks.
   */
  canScheduleMaintenance?: boolean;
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
  onTagChange,
  busyIds = NO_SET,
  checkingIds = NO_SET,
  checkResults = NO_MAP,
  rowErrors = NO_MAP,
  createOpen = false,
  onCreateOpenChange,
  canScheduleMaintenance = false,
}: MonitorsViewProps) {
  const [query, setQuery] = useState("");
  const [type, setType] = useState<string>("");
  /** "" = every monitor, "active", "paused". The default shows everything,
   *  which is precisely where this page differs from the dashboard. */
  const [pausedFilter, setPausedFilter] = useState<string>("");
  /*
   * Name by default, where the list used to arrive in the order monitors were
   * created. Creation order is an accident of history: nobody looks for
   * "Docs" by remembering it was the fourth one added.
   */
  const [sort, setSort] = useState<InventorySort>("name");
  /* The dashboard's tag filters, from the same component and the same rule
     for a selection whose tag has since vanished. */
  const [tags, setTags] = useState<TagSelection>({});
  const facets = useMemo(() => tagFacets(monitors), [monitors]);
  const liveTags = useMemo(() => liveTagSelection(facets, tags), [facets, tags]);
  // Pruned, not only masked, as on the dashboard: a pair kept in state would
  // filter again by itself when its value came back.
  if (!sameTagSelection(tags, liveTags)) setTags(liveTags);
  const [maintenanceOpen, setMaintenanceOpen] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [tagOpen, setTagOpen] = useState(false);
  useEffect(() => registerNavigationCleanup(() => setTagOpen(false)), []);
  const [selected, setSelected] = useState<ReadonlySet<string>>(NO_SET);
  const selectedIds = monitors.filter((m) => selected.has(m.id)).map((m) => m.id);
  const selectMonitor = useCallback((id: string, checked: boolean) => {
    setSelected((held) => {
      const next = new Set(held);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);
  // Removal and permission loss discard selection, never retain ghost IDs.
  if (selected.size !== selectedIds.length || (onTagChange === undefined && selected.size > 0)) {
    setSelected(onTagChange === undefined ? NO_SET : new Set(selectedIds));
  }
  if (onTagChange === undefined && tagOpen) setTagOpen(false);

  const visible = useMemo(() => {
    let out = filterByType(filterMonitors(monitors, query), type);
    if (pausedFilter === "active") out = out.filter((m) => m.enabled);
    if (pausedFilter === "paused") out = out.filter((m) => !m.enabled);
    return sortInventory(filterByTags(out, liveTags), sort);
  }, [monitors, query, type, pausedFilter, sort, liveTags]);

  const visibleIds = new Set(visible.map((m) => m.id));
  const hiddenSelectionCount = selectedIds.filter((id) => !visibleIds.has(id)).length;
  const visibleSelectedCount = selectedIds.length - hiddenSelectionCount;
  const allVisibleSelected = visible.length > 0 && visibleSelectedCount === visible.length;
  const someVisibleSelected = visibleSelectedCount > 0 && !allVisibleSelected;
  const deleteTarget = monitors.find((m) => m.id === confirming) ?? null;
  /*
   * Bulk pause and resume act on the selection, hidden rows included — the
   * same set Manage tags acts on, and the count beside the boxes says so.
   *
   * Each is a run of the row's own pause or resume, not one request: there is
   * no bulk endpoint, and a series of independent writes must not present
   * itself as one success. Each row shows its own busy state and its own
   * failure, exactly as if it had been pressed by hand. A monitor already in
   * the asked-for state is left alone, so the count on the button is the
   * number of monitors it will change.
   */
  const selectedMonitors = monitors.filter((m) => selected.has(m.id));
  const toPause = selectedMonitors.filter((m) => m.enabled && !busyIds.has(m.id));
  const toResume = selectedMonitors.filter((m) => !m.enabled && !busyIds.has(m.id));

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
       * The filter field, the filters and the count, together in the page
       * toolbar (SUB-182). The masthead keeps one search for the whole
       * product — the command menu — so this field is named for what it is: a
       * filter over the list beneath it.
       *
       * The counter travels with the filters, and that is not tidiness.
       * "3 of 3 shown" is the filter's honesty — it says you are looking at a
       * selection rather than at everything — so it belongs beside the
       * controls that make the claim true.
       *
       * Portalled rather than passed up as props, so the filter state stays
       * inside the screen that filters. See `ToolbarTools`.
       */}
      <ToolbarTools>
        <div className="tb-group">
          <label className="shell-search">
            <span className="sr-only">Filter monitors</span>
            <SearchIcon />
            <input
              type="search"
              /* `shell-search-input` pins the 16px minimum at every width.
                 `.input` drops to 14px above 640px, which is fine for a
                 form nobody types into on a phone in landscape and wrong for a
                 search box: iOS Safari zooms the page on focus below 16px and
                 leaves the reader scrolled sideways (DESIGN.md §13). */
              className="shell-search-input"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Filter monitors…"
            />
          </label>

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

          <TagFilters
            facets={facets}
            selected={tags}
            onChange={(key, value) =>
              setTags((current) => ({ ...current, [key]: value }))
            }
          />

          {/* Order, not a filter: it changes where rows are, never which are
              shown, so it sits after everything that narrows. */}
          <label className="tb-field">
            <span className="tb-label">Sort</span>
            <select
              className="tb-select"
              value={sort}
              onChange={(event) => setSort(event.target.value as InventorySort)}
            >
              {INVENTORY_SORTS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
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
        icon={<IconList />}
        headingLevel={2}
        /*
         * `h2`, under the page frame's visible `Monitors` `h1` (SUB-182).
         * The card was the page's heading for a while (SUB-138); the level is
         * a fact about where the card sits rather than a style choice.
         */
        note={describeInventory(monitors)}
        action={
          <div className="bulk-tags-actions">
          {/*
           * Maintenance opens from here, at the top of the page, where it
           * used to be a collapsed card under every monitor. It is planned
           * minutes before a change, so it has to be found quickly — and it
           * is offered to a viewer too, read-only.
           */}
          <button
            type="button"
            className="button"
            aria-label="Maintenance"
            onClick={() => setMaintenanceOpen(true)}
          >
            <IconClock />
            Maintenance
          </button>
          {/* Pause and resume for the selection, beside Manage tags, the
              other action on it. Only while something is selected: at rest
              they would be two disabled buttons on every visit. */}
          {onTogglePaused !== undefined && toPause.length > 0 && (
            <button type="button" className="button" aria-label={`Pause ${toPause.length} selected`}
              onClick={() => toPause.forEach((m) => onTogglePaused(m.id, true))}>
              <IconPause />
              Pause {toPause.length}
            </button>
          )}
          {onTogglePaused !== undefined && toResume.length > 0 && (
            <button type="button" className="button" aria-label={`Resume ${toResume.length} selected`}
              onClick={() => toResume.forEach((m) => onTogglePaused(m.id, false))}>
              <IconPlay />
              Resume {toResume.length}
            </button>
          )}
          {/* Disabled, not hidden, with nothing to tag: the header keeps its
              shape while the list loads, and an empty inventory says why
              right below it. */}
          {onTagChange && <button type="button" className="button" disabled={loading || error !== null || monitors.length === 0} onClick={() => setTagOpen(true)}>Manage tags</button>}
          {onCreateOpenChange === undefined ? undefined : (
            <button
              type="button"
              className="button button--primary"
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
          )}
          </div>
        }
      >
        {onTagChange && !loading && error === null && monitors.length > 0 && <div className="bulk-tags-selection">
          {/* One box for the visible rows, in the three states a group box
              has: all of them, none, or some (a dash, announced as "mixed").
              Checking it adds the visible rows; unchecking it removes only
              them, so a selection hidden by a filter survives both. */}
          <Checkbox checked={allVisibleSelected} indeterminate={someVisibleSelected} disabled={visible.length === 0}
            onChange={() => setSelected(allVisibleSelected ? new Set([...selected].filter((id) => !visibleIds.has(id))) : new Set([...selected, ...visible.map((m) => m.id)]))}>
            Select all visible ({visible.length})
          </Checkbox>
          {/* The rest of the bar only once there is a selection to describe:
              "0 selected" and a disabled Clear above every visit to the page
              said nothing, twice. The live region stays mounted so the count
              is announced when the first box is ticked. */}
          {selectedIds.length > 0 && <button type="button" className="button" onClick={() => setSelected(NO_SET)}>Clear selection</button>}
          {/* The same count as the toolbar's "n of m shown", and drawn the
              same: a count about the list is helper text, not a sentence in
              body ink beside the controls it counts for. */}
          <p className="tb-count" role="status">{selectedIds.length === 0 ? "" : `${selectedIds.length} selected${hiddenSelectionCount > 0 ? ` · ${hiddenSelectionCount} hidden by filters` : ""}`}</p>
        </div>}
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
          <p className="field-help">
            {loading ? "Loading monitors…" : "The list could not be loaded."}
          </p>
        ) : monitors.length === 0 ? (
          <EmptyState
            headingLevel={3}
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
            headingLevel={3}
            query={query}
            totalCount={monitors.length}
            filtered={type !== "" || pausedFilter !== "" || Object.keys(liveTags).length > 0}
          />
        ) : (
          <div className="inv-table">
          <MonitorInventoryHead />
          <ul className="inv-list">
            {visible.map((monitor) => (
              <MonitorInventoryRow
                key={monitor.id}
                monitor={monitor}
                selected={selected.has(monitor.id)}
                onSelect={onTagChange === undefined ? undefined : selectMonitor}
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
          </div>
        )}
      </Card>

      <MaintenanceDrawer
        open={maintenanceOpen}
        onClose={() => setMaintenanceOpen(false)}
        monitors={monitors}
        canWrite={canScheduleMaintenance}
      />

      {tagOpen && onTagChange && <BulkTagDrawer selectedIds={selectedIds} onChange={onTagChange} onClose={() => setTagOpen(false)} />}

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
          <Card title={editing.name} icon={<IconPulse />} headingLevel={3}>
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
          consequence={monitorDeleteConsequence(deleteTarget.name)}
          onConfirm={() => {
            onDelete?.(deleteTarget.id);
            setConfirming(null);
          }}
        />
      )}

      {onTogglePaused === undefined && !loading && monitors.length > 0 && (
        <p className="field-help">
          <StateChip>read only</StateChip> This account may read the inventory
          but not change it.
        </p>
      )}
    </section>
  );
}
