import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { BeakerIcon, SidebarIcon, SearchIcon } from "./icons";

/**
 * The masthead: what works on every screen, and nothing else (SUB-182).
 *
 * Sticky, so the controls that are always there are always in reach. It
 * carries no page title — the sidebar already says where you are, and a
 * heading repeated in two pieces of chrome is the kind of decoration rule 1
 * rejects.
 *
 * The connection banner is rendered *below* this bar by the dashboard itself:
 * a warning that the numbers are frozen has to be read before the numbers,
 * and the dashboard is the only screen that has those numbers.
 *
 * **The rule: a control is in this bar only on a screen where it does
 * something.** Left: the sidebar toggle, then search. Right: the workbench and
 * the theme. All four work on every route, the monitor detail page included,
 * so the bar is the same everywhere as a consequence of the rule rather than
 * as a rule of its own.
 *
 * It used to be the other way round (SUB-138): the bar was held identical on
 * every screen, and so the dashboard's layout switcher sat on Monitors,
 * Incidents, Notifications and Settings, where pressing it changed nothing on
 * screen. A control that does nothing where it is shown teaches people the
 * bar is decoration. The switcher is now the dashboard's own, in its page
 * toolbar.
 *
 * **Search is one entry, and it is global.** It opens the command menu, which
 * finds any monitor and any destination from any screen — the detail page
 * included, which used to lose search altogether. The field beside it that
 * narrowed the current list was a second search entry in the same place;
 * narrowing a list is that screen's tool, so it is a filter in that screen's
 * toolbar now.
 *
 * The add-monitor button left this bar for the same reason: pressed on
 * Notifications it opened a drawer for adding a *monitor*, on a screen about
 * channels. Monitors carries an "Add monitor" button in its own card header,
 * and the empty dashboard carries the loud version.
 *
 * Tools that belong to one route live in `PageToolbar`, the bar underneath.
 */

export type TopbarProps = {
  sidebarCollapsed: boolean;
  /** True below the breakpoint, where this button opens a drawer. */
  narrow?: boolean;
  onToggleSidebar: () => void;
  themePreference: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  workbenchOpen: boolean;
  onToggleWorkbench: () => void;
  /** Opens the command menu: the masthead's search. */
  onOpenCommands?: () => void;
  /**
   * Anything else genuinely global, which is currently nothing. Kept as the
   * escape hatch for a control that works on every screen and does not fit
   * the groups below; a control for one screen goes to `PageToolbar`.
   */
  children?: ReactNode;
};

export function Topbar({
  sidebarCollapsed,
  narrow = false,
  onToggleSidebar,
  themePreference,
  onThemeChange,
  workbenchOpen,
  onToggleWorkbench,
  onOpenCommands,
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

      {/*
       * Search: one entry, on every screen (SUB-182).
       *
       * A button drawn as a field, not an input. What it opens is a dialog
       * with its own input, and an input here would have to hand its first
       * keystroke over to that one — a field that moves your cursor somewhere
       * else as you type is a stranger control than a button that says what
       * it opens.
       *
       * Named "Search", which is also the word on it: the visible label is
       * the accessible name (WCAG 2.5.3), so someone who says "click Search"
       * reaches it. `aria-haspopup` says a dialog follows, `aria-keyshortcuts`
       * carries the shortcut, and the keycap only draws it.
       */}
      {onOpenCommands && (
        <button
          type="button"
          className="shell-search shell-command-launcher"
          aria-label="Search"
          aria-haspopup="dialog"
          aria-keyshortcuts="Control+K Meta+K"
          title="Search monitors and pages — Ctrl/Cmd + K"
          onClick={onOpenCommands}
        >
          <SearchIcon />
          <span className="shell-command-text">Search…</span>
          <span className="shell-search-kbd" aria-hidden="true">⌘K</span>
        </button>
      )}

      {children}

      <div className="shell-topbar-right">
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
