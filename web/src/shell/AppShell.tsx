import type { ReactNode, RefObject } from "react";
import { NavDrawer } from "./NavDrawer";
import { Sidebar } from "./Sidebar";

/**
 * The frame: sidebar beside content, both scrolling independently.
 *
 * Presentational, like `Dashboard`. It owns no preference and no shortcut —
 * `App` holds those and hands down the booleans this needs — so the frame can
 * be rendered in a test or a workbench without a storage stub.
 *
 * There is no frame around the status wall: that layout deliberately has no
 * chrome, so the shell renders the wall *instead of* this component.
 *
 * **The sidebar lives in two places, by width.** On a laptop it is a grid
 * track that narrows to a 56px rail. On a phone that track is 15% of the
 * screen spent on navigation you are not using, so it leaves the grid
 * altogether and the same navigation is available as an overlay drawer
 * (DESIGN.md §13). Which one is in play is decided by `narrow`, from the one
 * breakpoint the product has — not by a second, independent rule here.
 */
export type AppShellProps = {
  sidebarCollapsed: boolean;
  /** True below the breakpoint: the rail is replaced by the drawer. */
  narrow?: boolean;
  /** Whether the phone drawer is open. Ignored when not narrow. */
  navOpen?: boolean;
  onNavClose?: () => void;
  /** Holds the element focus returns to when the drawer closes; see NavDrawer. */
  navReturnFocusRef?: RefObject<HTMLElement | null>;
  instance?: string;
  /** Which built destination is on screen; lights the matching nav item. */
  current?: "dashboard" | "incidents" | "monitor";
  /** Client-side navigation from the sidebar and the drawer. */
  onNavigate?: (route: "dashboard" | "incidents") => void;
  /** The signed-in address, shown in the navigation footer. */
  account?: string;
  onSignOut?: () => void;
  /** The topbar, rendered sticky above the content column. */
  topbar: ReactNode;
  /**
   * Focus target for a client-side navigation, put on `<main>`.
   *
   * `App` owns it because `App` owns the route: the shell does not know that
   * a navigation happened, only that something new is in `children`.
   */
  mainRef?: RefObject<HTMLElement | null>;
  children: ReactNode;
};

export function AppShell({
  sidebarCollapsed,
  narrow = false,
  navOpen = false,
  onNavClose,
  navReturnFocusRef,
  instance,
  current,
  onNavigate,
  account,
  onSignOut,
  topbar,
  mainRef,
  children,
}: AppShellProps) {
  const drawerOpen = narrow && navOpen;

  return (
    <>
      {/*
       * The skip link, and the first focusable thing in the document.
       *
       * The rail is five links and the topbar is seven controls, so reaching
       * the monitors with the keyboard cost a dozen Tab presses on every
       * screen. It is visually hidden until focused — hiding it permanently
       * would make it a trap for a sighted keyboard user, who would watch
       * focus vanish for one stop — and it is outside the `inert` wrapper
       * below on purpose: while the drawer is open there is nothing to skip
       * to, and a link into an inert region does nothing.
       */}
      {!drawerOpen && (
        <a className="shell-skip" href="#shell-main">
          Skip to monitors
        </a>
      )}
      <div
        className="shell"
        data-collapsed={sidebarCollapsed ? "true" : "false"}
        data-narrow={narrow ? "true" : "false"}
        /*
         * While the drawer is open the page underneath is inert: not
         * focusable, not clickable, not reachable by assistive technology.
         * This is the platform's own mechanism rather than a hand-written
         * focus trap — it cannot miss a control that was added later, and it
         * still lets Tab reach the browser's chrome, which a keyboard user is
         * as entitled to as a mouse user is.
         */
        inert={drawerOpen}
      >
        {/*
         * The rail is not rendered at all on a phone. Hiding it in CSS would
         * leave five links in the tab order underneath the drawer, which is
         * the quiet version of the same bug the `inert` above prevents.
         */}
        {!narrow && (
          <Sidebar
            collapsed={sidebarCollapsed}
            instance={instance}
            current={current}
            onNavigate={onNavigate}
            account={account}
            onSignOut={onSignOut}
          />
        )}
        <div className="shell-main">
          {topbar}
          {/*
           * `<main>` starts here, not around the whole grid: the sidebar is
           * navigation, and including it would make "skip to main content"
           * skip to the thing you were trying to skip past.
           */}
          {/*
           * `tabIndex={-1}` so a route change can move focus here (see
           * useRouteFocus) and so the skip link lands somewhere focus can
           * actually rest. Negative, never 0: this must be a target, not a
           * stop on the way to the content inside it.
           */}
          <main
            id="shell-main"
            ref={mainRef}
            tabIndex={-1}
            className="shell-content"
          >
            {children}
          </main>
        </div>
      </div>

      {narrow && (
        <NavDrawer
          open={navOpen}
          onClose={onNavClose ?? (() => {})}
          returnFocusRef={navReturnFocusRef}
          instance={instance}
          current={current}
          onNavigate={onNavigate}
          account={account}
          onSignOut={onSignOut}
        />
      )}
    </>
  );
}
