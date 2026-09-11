import { useCallback, useRef, useState } from "react";
import { useTheme } from "./theme/useTheme";
import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";
import { LiveDashboardRoot } from "./live/LiveDashboard";
import { AppShell } from "./shell/AppShell";
import { Topbar } from "./shell/Topbar";
import { useShellPreferences } from "./shell/useShellPreferences";
import { useShellShortcuts } from "./shell/useShortcuts";
import { effectiveLayout } from "./shell/preferences";
import { useCompactViewport } from "./layout/useMediaQuery";

/**
 * The application shell.
 *
 * You land on the real dashboard. That is the whole change from the tab bar
 * this replaces: a product whose front door is a component gallery reads as a
 * demo, and the components were never the thing being demonstrated.
 *
 * This component owns the two shell preferences and the two shortcuts;
 * everything below it is presentational and takes them as props.
 *
 * The workbench survives as a side track behind the beaker button. Judging a
 * component in isolation, in both themes and in every status is something the
 * live screen cannot do — it only ever shows the states the server happens to
 * be in — but it is a developer tool and does not belong in the navigation.
 */
export default function App() {
  const { preference, setPreference } = useTheme();
  const { layout, setLayout, sidebarCollapsed, toggleSidebar } =
    useShellPreferences(window.localStorage);
  const [workbenchOpen, setWorkbenchOpen] = useState(false);

  const [navOpen, setNavOpen] = useState(false);
  /*
   * The control that opened the drawer, captured in the click handler.
   *
   * It cannot be looked up after the fact: opening the drawer marks the shell
   * `inert` in the same commit, and the HTML focus fixup rule immediately moves
   * focus off the now-inert topbar button onto `body`. A ref rather than state
   * because nothing renders from it — storing it in state would re-render the
   * whole app to remember a DOM node.
   */
  const navOpenerRef = useRef<HTMLElement | null>(null);

  const narrow = useCompactViewport();
  const shown = effectiveLayout(layout, narrow);
  const isWall = shown === "wall" && !workbenchOpen;

  const leaveWall = useCallback(() => setLayout("rows"), [setLayout]);
  const toggleWorkbench = useCallback(() => setWorkbenchOpen((open) => !open), []);
  const closeNav = useCallback(() => setNavOpen(false), []);

  /*
   * One button, two meanings, decided by width. On a laptop it collapses the
   * rail; on a phone there is no rail, so it opens the drawer. Both are "show
   * or hide the navigation" — the control does not change, only where the
   * navigation lives (DESIGN.md §13).
   */
  const toggleNav = useCallback(() => {
    if (narrow) {
      // Captured here, before the state update, for the reason on navOpenerRef.
      // Reading `navOpen` rather than doing this inside the updater keeps the
      // updater pure — React may call it twice.
      if (!navOpen) {
        navOpenerRef.current =
          document.activeElement instanceof HTMLElement ? document.activeElement : null;
      }
      setNavOpen((open) => !open);
      return;
    }
    toggleSidebar();
  }, [narrow, navOpen, toggleSidebar]);

  /*
   * Esc has a queue, and the drawer is at the front of it: it is the newest
   * and most modal thing on screen, so it must be dismissed before Esc means
   * "leave the wall" or "leave the workbench". Passing undefined when there
   * is nothing to leave keeps the key's browser meaning everywhere else.
   */
  useShellShortcuts({
    onToggleSidebar: toggleNav,
    onEscape: navOpen
      ? closeNav
      : isWall
        ? leaveWall
        : workbenchOpen
          ? toggleWorkbench
          : undefined,
  });

  if (isWall) {
    return <LiveDashboardRoot layout="wall" onExitWall={leaveWall} />;
  }

  return (
    <AppShell
      sidebarCollapsed={sidebarCollapsed}
      narrow={narrow}
      navOpen={navOpen}
      onNavClose={closeNav}
      navReturnFocusRef={navOpenerRef}
      topbar={
        <Topbar
          sidebarCollapsed={narrow ? !navOpen : sidebarCollapsed}
          narrow={narrow}
          onToggleSidebar={toggleNav}
          layout={layout}
          effectiveLayout={shown}
          onLayoutChange={setLayout}
          themePreference={preference}
          onThemeChange={setPreference}
          workbenchOpen={workbenchOpen}
          onToggleWorkbench={toggleWorkbench}
        />
      }
    >
      {workbenchOpen ? <Workbench /> : <LiveDashboardRoot layout={shown} />}
    </AppShell>
  );
}

/** The side track: every component, every state, both themes. */
function Workbench() {
  return (
    <div className="flex flex-col gap-10">
      <p className="text-[12.5px] text-ink-3">
        Component workbench — fixtures, not live data. Press Esc to go back to the dashboard.
      </p>
      <DashboardWorkbench />
      <HeartbeatGallery />
      <TokenSheet />
    </div>
  );
}
