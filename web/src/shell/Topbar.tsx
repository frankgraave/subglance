import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { LayoutSwitcher } from "./LayoutSwitcher";
import { BeakerIcon, PlusIcon, SidebarIcon } from "./icons";
import type { LayoutId } from "./preferences";

/**
 * The toolbar: collapse the sidebar, choose a layout, switch theme.
 *
 * Sticky, because at 200 monitors the layout switch is otherwise a scroll away
 * from the rows it changes. It carries no page title — the sidebar already
 * says where you are, and a heading repeated in two pieces of chrome is the
 * kind of decoration rule 1 rejects.
 *
 * The connection banner is rendered *above* this bar by the dashboard itself:
 * a warning that the numbers are frozen has to be read before the numbers, and
 * this is the last chrome before them.
 */

export type TopbarProps = {
  sidebarCollapsed: boolean;
  /** True below the breakpoint, where this button opens a drawer. */
  narrow?: boolean;
  onToggleSidebar: () => void;
  layout: LayoutId;
  effectiveLayout?: LayoutId;
  onLayoutChange: (next: LayoutId) => void;
  themePreference: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  workbenchOpen: boolean;
  onToggleWorkbench: () => void;
  /** Opens the add-monitor form. Omitted where there is nothing to add to. */
  onAddMonitor?: () => void;
  addOpen?: boolean;
  /** Optional extra controls, e.g. a search field owned by the page. */
  children?: ReactNode;
};

export function Topbar({
  sidebarCollapsed,
  narrow = false,
  onToggleSidebar,
  layout,
  effectiveLayout,
  onLayoutChange,
  themePreference,
  onThemeChange,
  workbenchOpen,
  onToggleWorkbench,
  onAddMonitor,
  addOpen = false,
  children,
}: TopbarProps) {
  return (
    <header className="shell-topbar">
      <button
        type="button"
        className="shell-icon-btn"
        onClick={onToggleSidebar}
        aria-expanded={!sidebarCollapsed}
        // The name says what pressing it will do, and on a phone that is not
        // the same action: there is no rail to collapse, there is a drawer to
        // open. A label that says "Collapse sidebar" beside a screen with no
        // sidebar is a small lie told by the one control that has to be
        // trusted to lead somewhere.
        //
        // The shortcut stays in the accessible name on the desktop, where a
        // keyboard exists: shortcuts that exist but are undiscoverable are a
        // known gap in DESIGN.md §12, and this is the cheapest half of it.
        aria-label={
          narrow
            ? sidebarCollapsed
              ? "Open navigation"
              : "Close navigation"
            : `${sidebarCollapsed ? "Expand" : "Collapse"} sidebar (Ctrl+B)`
        }
        title={
          narrow
            ? sidebarCollapsed
              ? "Open navigation"
              : "Close navigation"
            : `${sidebarCollapsed ? "Expand" : "Collapse"} sidebar — Ctrl/Cmd + B`
        }
      >
        <SidebarIcon />
      </button>

      {children}

      <div className="shell-topbar-right">
        {/*
         * First in the group, and an icon button like the rest.
         *
         * Product principle 2 starts with finding this control: a monitoring
         * tool whose primary action is behind a settings page fails the sixty
         * seconds before the form is even reached. It stays an icon rather
         * than a filled button because a permanently green button on every
         * screen becomes wallpaper (DESIGN.md §7.1) — the empty dashboard
         * carries the loud version of the same action instead.
         */}
        {onAddMonitor !== undefined && (
          <button
            type="button"
            className="shell-icon-btn"
            onClick={onAddMonitor}
            aria-pressed={addOpen}
            aria-label="Add a monitor"
            title="Add a monitor"
          >
            <PlusIcon />
          </button>
        )}

        <LayoutSwitcher layout={layout} effective={effectiveLayout} onChange={onLayoutChange} />

        {/*
         * The workbench survives, as a side track rather than a tab beside the
         * product. Judging a component in isolation and in both themes is
         * something the live screen cannot do — it only ever shows the states
         * the server happens to be in — but it is a developer tool, and the
         * first thing you land on must be the real dashboard.
         */}
        <button
          type="button"
          className="shell-icon-btn"
          onClick={onToggleWorkbench}
          aria-pressed={workbenchOpen}
          aria-label="Component workbench"
          title="Component workbench — every state, both themes"
        >
          <BeakerIcon />
        </button>

        <ThemeToggle preference={themePreference} onChange={onThemeChange} />
      </div>
    </header>
  );
}
