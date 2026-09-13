import { useCallback, useRef, useState } from "react";
import { useTheme } from "./theme/useTheme";
import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";
import { AddMonitor } from "./monitors/AddMonitor";
import { LiveDashboardRoot } from "./live/LiveDashboard";
import { LiveMonitorDetailRoot } from "./live/LiveMonitorDetail";
import { createQueryClient } from "./live/queryClient";
import { useRoute } from "./shell/useRoute";
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
  const [addOpen, setAddOpen] = useState(false);
  const { route, navigate } = useRoute();
  /*
   * One query client for both screens, created here rather than inside each
   * root.
   *
   * The two roots each fall back to their own client when given none, which is
   * right for a test mounting one in isolation and wrong for the app: the
   * dashboard unmounts when a monitor opens, and with a per-root client its
   * cache would go with it. Every trip back to the list would then refetch
   * every monitor and replay the SSE handshake, so the Back button — the most
   * used control on this screen — would be the slowest one. Sharing the cache
   * makes both directions instant.
   */
  const [queryClient] = useState(createQueryClient);

  const openMonitor = useCallback(
    (id: string) => {
      setWorkbenchOpen(false);
      setAddOpen(false);
      navigate({ name: "monitor", id });
      // A new page starts at the top. Without this the browser keeps the
      // dashboard's scroll offset, so opening a monitor from row 80 lands
      // halfway down its incident list.
      window.scrollTo(0, 0);
    },
    [navigate],
  );
  const showDashboard = useCallback(() => navigate({ name: "dashboard" }), [navigate]);

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
  const onDetail = route.name === "monitor";
  /*
   * The wall is a dashboard layout, so it cannot be showing while a single
   * monitor is open. Without this, choosing the wall from the detail page
   * would replace it with a chrome-less grid and no way back.
   */
  const isWall = shown === "wall" && !workbenchOpen && !onDetail;

  const leaveWall = useCallback(() => setLayout("rows"), [setLayout]);
  /*
   * The workbench and the add form are mutually exclusive, and the toggles —
   * not the render branch — are where that is enforced.
   *
   * The branch below can only show one of them, and it picks the workbench. So
   * opening the workbench and then pressing Add a monitor used to set
   * `addOpen`, light the button up as pressed, and leave the workbench on
   * screen: a control reporting a state the page does not have. Esc then closed
   * a form nobody could see. Closing the other mode here keeps "what the
   * buttons claim" and "what is rendered" the same thing, which asserting it
   * in the branch alone cannot do.
   */
  const toggleWorkbench = useCallback(() => {
    setAddOpen(false);
    setWorkbenchOpen((open) => !open);
  }, []);
  const toggleAdd = useCallback(() => {
    setWorkbenchOpen(false);
    setAddOpen((open) => !open);
  }, []);
  /*
   * Closing on success rather than navigating to the new monitor.
   *
   * The dashboard is where the answer is: the monitor appears in the list
   * within one heartbeat, which is the confirmation that the thing works.
   * Jumping straight to its detail view would trade that confirmation for a
   * page that has nothing on it yet — no checks, no uptime, no incidents. A
   * toast would say the same thing less durably (DESIGN.md §7.6).
   */
  const closeAdd = useCallback(() => setAddOpen(false), []);
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
   * "leave the add form", "leave the wall" or "leave the workbench". Passing
   * undefined when there is nothing to leave keeps the key's browser meaning
   * everywhere else.
   */
  useShellShortcuts({
    onToggleSidebar: toggleNav,
    onEscape: navOpen
      ? closeNav
      : addOpen
        ? closeAdd
        : isWall
          ? leaveWall
          : workbenchOpen
            ? toggleWorkbench
            : onDetail
              ? // Last in the queue, because the detail view is a place rather
                // than an overlay: anything layered on top of it must be
                // dismissed before Esc means "leave this monitor".
                showDashboard
              : undefined,
  });

  if (isWall) {
    return <LiveDashboardRoot client={queryClient} layout="wall" onExitWall={leaveWall} />;
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
          onAddMonitor={toggleAdd}
          addOpen={addOpen}
        />
      }
    >
      {workbenchOpen ? (
        <Workbench />
      ) : addOpen ? (
        <AddMonitor onCreated={closeAdd} onCancel={closeAdd} />
      ) : onDetail ? (
        <LiveMonitorDetailRoot
          client={queryClient}
          id={route.id}
          onBack={showDashboard}
        />
      ) : (
        <LiveDashboardRoot
          client={queryClient}
          layout={shown}
          onOpenMonitor={openMonitor}
        />
      )}
    </AppShell>
  );
}

/** The side track: every component, every state, both themes. */
function Workbench() {
  return (
    <div className="flex flex-col gap-10">
      <p className="text-helper text-ink-3">
        Component workbench — fixtures, not live data. Press Esc to go back to the dashboard.
      </p>
      <DashboardWorkbench />
      <HeartbeatGallery />
      <TokenSheet />
    </div>
  );
}
