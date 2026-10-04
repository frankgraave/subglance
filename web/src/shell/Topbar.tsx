import type { ReactNode } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import type { ThemePreference } from "../theme/theme";
import { SidebarIcon, SearchIcon } from "./icons";
import { PAGE_TITLE_ID } from "./pages";

/**
 * The masthead: the page's title, and what works on every screen (SUB-182,
 * SUB-207).
 *
 * Sticky, so the controls that are always there are always in reach.
 *
 * **The page title lives here, as the page's only `h1`.** It used to be drawn
 * at the top of the content, under this bar and under the page toolbar, so a
 * list screen stacked three layers — chrome, controls, title — before the
 * first card. In the bar it costs no height at all, it is the same word on
 * every route as the sidebar and the tab (all three read `PAGE_TITLES`), and
 * it never scrolls away. On a monitor's page it is a breadcrumb: the section
 * as a link, then the monitor's name as the heading.
 *
 * The connection banner is rendered *below* this bar by the dashboard itself:
 * a warning that the numbers are frozen has to be read before the numbers,
 * and the dashboard is the only screen that has those numbers.
 *
 * **The rule: a control is in this bar only on a screen where it does
 * something.** Left: the sidebar toggle, then the title. Right: search and
 * the theme. All of them work on every route, the monitor detail page
 * included, so the bar is the same everywhere as a consequence of the rule
 * rather than as a rule of its own. On a phone the theme leaves the bar —
 * it is one tap away in Settings and in the command menu — so the title keeps
 * the room, and search is one magnifier.
 *
 * The component workbench used to sit on the right as a beaker button. It
 * worked on every screen, but it is a developer tool drawn on fixture data,
 * and a control everybody meets for something almost nobody needs is noise
 * at best and a page of fake monitors at worst (SUB-193). It lives at
 * `/workbench` now, typed rather than linked.
 *
 * It used to be the other way round (SUB-138): the bar was held identical on
 * every screen, and so the dashboard's layout switcher sat on Monitors,
 * Incidents, Notifications and Settings, where pressing it changed nothing on
 * screen. A control that does nothing where it is shown teaches people the
 * bar is decoration. The switcher is now the dashboard's own.
 *
 * **Search is one entry, and it is global.** It opens the command menu, which
 * finds any monitor and any destination from any screen — the detail page
 * included, which used to lose search altogether. The field beside it that
 * narrowed the current list was a second search entry in the same place;
 * narrowing a list is that screen's tool, not this bar's.
 *
 * The add-monitor button left this bar for the same reason: pressed on
 * Notifications it opened a drawer for adding a *monitor*, on a screen about
 * channels. Monitors carries an "Add monitor" button in its own card header,
 * and the empty dashboard carries the loud version.
 */

/** The section a page sits under, drawn before its title as a link. */
export type TitleParent = {
  label: string;
  href: string;
  onNavigate: () => void;
};

export type TopbarProps = {
  sidebarCollapsed: boolean;
  /** True below the breakpoint, where this button opens a drawer. */
  narrow?: boolean;
  onToggleSidebar: () => void;
  themePreference: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  /** Opens the command menu: the masthead's search. */
  onOpenCommands?: () => void;
  /**
   * The page's title: the `h1` of every screen. `null` draws no heading, for
   * a frame with nothing to name (a test mounting the bar on its own).
   */
  title?: string | null;
  /** On a child page, the section it belongs to, drawn as a link before the title. */
  parent?: TitleParent;
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
  onOpenCommands,
  title = null,
  parent,
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
       * The title, after the toggle and a hairline (SUB-207).
       *
       * One line that ends in an ellipsis rather than wrapping: the bar is
       * one row on every width, and a monitor's hostname is the one title
       * long enough to need cutting. The full text stays in the heading, so
       * a screen reader and the tab (which says the same name) both have it.
       *
       * On a child page the parent is a real link, not text: it is where the
       * rail already says you are, and “up one level” is the move the
       * breadcrumb is for. The slash between them is drawn, not read.
       */}
      {title !== null && (
        <div className="shell-heading">
          {parent && (
            <>
              <a
                className="shell-crumb"
                href={parent.href}
                onClick={(event) => {
                  if (
                    event.defaultPrevented ||
                    event.button !== 0 ||
                    event.metaKey ||
                    event.ctrlKey ||
                    event.shiftKey ||
                    event.altKey
                  ) {
                    return;
                  }
                  event.preventDefault();
                  parent.onNavigate();
                }}
              >
                {parent.label}
              </a>
              <span className="shell-crumb-sep" aria-hidden="true">/</span>
            </>
          )}
          <h1 id={PAGE_TITLE_ID} className="shell-title" title={title}>
            {title}
          </h1>
        </div>
      )}

      {children}

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
       * carries the shortcut, and the keycap only draws it. Below the tablet
       * rung the word and the keycap go and the magnifier stays, so the title
       * keeps the bar's width (SUB-207); the name stays on the button.
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

      {/* Not on a phone: the bar there is the menu, the title and one
          magnifier. The theme is in Settings and in the command menu. */}
      {!narrow && (
        <div className="shell-topbar-right">
          <ThemeToggle preference={themePreference} onChange={onThemeChange} />
        </div>
      )}
    </header>
  );
}
