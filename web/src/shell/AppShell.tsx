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
  /** The topbar, rendered sticky above the content column. */
  topbar: ReactNode;
  children: ReactNode;
};

export function AppShell({
  sidebarCollapsed,
  narrow = false,
  navOpen = false,
  onNavClose,
  navReturnFocusRef,
  instance,
  topbar,
  children,
}: AppShellProps) {
  const drawerOpen = narrow && navOpen;

  return (
    <>
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
        {!narrow && <Sidebar collapsed={sidebarCollapsed} instance={instance} />}
        <div className="shell-main">
          {topbar}
          {/*
           * `<main>` starts here, not around the whole grid: the sidebar is
           * navigation, and including it would make "skip to main content"
           * skip to the thing you were trying to skip past.
           */}
          <main className="shell-content">{children}</main>
        </div>
      </div>

      {narrow && (
        <NavDrawer
          open={navOpen}
          onClose={onNavClose ?? (() => {})}
          returnFocusRef={navReturnFocusRef}
          instance={instance}
        />
      )}
    </>
  );
}
