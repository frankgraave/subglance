import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { LayoutSwitcher } from "./LayoutSwitcher";
import { BeakerIcon, SidebarIcon } from "./icons";
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
  onToggleSidebar: () => void;
  layout: LayoutId;
  effectiveLayout?: LayoutId;
  onLayoutChange: (next: LayoutId) => void;
  themePreference: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  workbenchOpen: boolean;
  onToggleWorkbench: () => void;
  /** Optional extra controls, e.g. a search field owned by the page. */
  children?: ReactNode;
};

export function Topbar({
  sidebarCollapsed,
  onToggleSidebar,
  layout,
  effectiveLayout,
  onLayoutChange,
  themePreference,
  onThemeChange,
  workbenchOpen,
  onToggleWorkbench,
  children,
}: TopbarProps) {
  return (
    <header className="shell-topbar">
      <button
        type="button"
        className="shell-icon-btn"
        onClick={onToggleSidebar}
        aria-expanded={!sidebarCollapsed}
        // The shortcut is in the accessible name, not only in a tooltip:
        // keyboard shortcuts that exist but are undiscoverable are listed as
        // a known gap in DESIGN.md §12, and this is the cheapest half of it.
        aria-label={`${sidebarCollapsed ? "Expand" : "Collapse"} sidebar (Ctrl+B)`}
        title={`${sidebarCollapsed ? "Expand" : "Collapse"} sidebar — Ctrl/Cmd + B`}
      >
        <SidebarIcon />
      </button>

      {children}

      <div className="shell-topbar-right">
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
