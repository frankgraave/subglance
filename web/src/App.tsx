import { useCallback, useEffect, useRef, useState } from "react";
import { SessionGate } from "./auth/SessionGate";
import { useSession } from "./auth/useSession";
import { useTheme } from "./theme/useTheme";
import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";
import { AddMonitor } from "./monitors/AddMonitor";
import { LiveDashboardRoot } from "./live/LiveDashboard";
import { LiveMonitorDetailRoot } from "./live/LiveMonitorDetail";
import { createQueryClient } from "./live/queryClient";
import { monitorsQueryKey } from "./live/api";
import { ErrorBoundary } from "./shell/ErrorBoundary";
import { useRoute } from "./shell/useRoute";
import { routePath } from "./shell/route";
import { useDocumentTitle } from "./shell/documentTitle";
import { useRouteFocus } from "./shell/useRouteFocus";
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
  const { session, onSignedIn, signOut, refresh } = useSession();
  const {
    layout,
    setLayout,
    cardColumns,
    setCardColumns,
    sidebarCollapsed,
    toggleSidebar,
  } = useShellPreferences(window.localStorage);
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
  const showDashboard = useCallback(
    () => navigate({ name: "dashboard" }),
    [navigate],
  );

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
   * The screen names itself and takes focus when the route changes
   * (SUB-100). Both hang off `routePath(route)` rather than the route object,
   * so the identity of a fresh `{ name: "dashboard" }` on every render cannot
   * re-fire them.
   *
   * The title is the route, not the monitor's name: `App` knows the id, and
   * the name lives behind a query inside `LiveMonitorDetailRoot`. Naming the
   * tab after the id would be worse than naming it after the screen, and
   * threading the name up here to title the page would make the whole shell
   * re-render on every heartbeat.
   */
  const path = routePath(route);
  useDocumentTitle(onDetail ? "Monitor" : "Monitors");
  const mainRef = useRef<HTMLElement | null>(null);
  useRouteFocus(path, mainRef);

  /*
   * The wall is a dashboard layout, so it cannot be showing while a single
   * monitor is open. Without this, choosing the wall from the detail page
   * would replace it with a chrome-less grid and no way back.
   */
  const isWall = shown === "wall" && !workbenchOpen && !onDetail;
  /*
   * What "a different screen" means for the inner error boundary: the route,
   * plus the two overlays the shell owns. Changing any of them remounts the
   * boundary and so clears a caught error — otherwise a crash on one screen
   * would latch and every screen after it would show the panel instead.
   */
  const boundaryKey = `${route.name}:${route.name === "monitor" ? route.id : ""}:${
    workbenchOpen ? "w" : addOpen ? "a" : ""
  }`;

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
  /*
   * Creating a monitor closes the form *and* invalidates the list.
   *
   * The comment above says the new monitor appears within one heartbeat, and
   * that was never true on its own: the stream only patches rows the list
   * already holds, so the first frame about a brand-new id used to be
   * dropped. The invalidation makes the promise good immediately instead of
   * waiting for a check to complete, which matters because this is the very
   * first thing anyone does with SubGlance and a screen that appears to do
   * nothing reads as a broken product.
   *
   * Both halves are kept: `useLiveMonitors` also resyncs on an unknown id, so
   * a monitor added in another tab shows up here too.
   */
  const onMonitorCreated = useCallback(() => {
    setAddOpen(false);
    void queryClient.invalidateQueries({ queryKey: monitorsQueryKey });
  }, [queryClient]);
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
          document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null;
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

  /*
   * Throw the cache away when the session ends.
   *
   * Without this, signing out and signing in as someone else repaints the
   * previous account's monitors from the cache before the first refetch
   * lands. On a shared instance that is a disclosure, not a glitch: the
   * second person sees names and targets they may have no rights to, and the
   * screen looks authoritative while it does.
   */
  const signedIn = session.state === "signedIn";
  useEffect(() => {
    if (!signedIn) queryClient.clear();
  }, [signedIn, queryClient]);

  const screen = isWall ? (
    <LiveDashboardRoot
      client={queryClient}
      layout="wall"
      onExitWall={leaveWall}
      mainRef={mainRef}
    />
  ) : (
    <AppShell
      sidebarCollapsed={sidebarCollapsed}
      mainRef={mainRef}
      narrow={narrow}
      navOpen={navOpen}
      onNavClose={closeNav}
      navReturnFocusRef={navOpenerRef}
      account={session.state === "signedIn" ? session.user.email : undefined}
      onSignOut={signOut}
      topbar={
        <Topbar
          sidebarCollapsed={narrow ? !navOpen : sidebarCollapsed}
          narrow={narrow}
          onToggleSidebar={toggleNav}
          layout={layout}
          effectiveLayout={shown}
          onLayoutChange={setLayout}
          cardColumns={cardColumns}
          onCardColumnsChange={setCardColumns}
          themePreference={preference}
          onThemeChange={setPreference}
          workbenchOpen={workbenchOpen}
          onToggleWorkbench={toggleWorkbench}
          onAddMonitor={toggleAdd}
          addOpen={addOpen}
        />
      }
    >
      {/*
       * A second boundary, inside the shell rather than around it.
       *
       * The outer one in main.tsx catches anything, but catching a dashboard
       * crash at the root costs the sidebar, the topbar and the navigation
       * along with it — so the recovery panel would appear on an otherwise
       * blank page with no way to go anywhere else. Here, the chrome survives
       * and only the failing screen is replaced. Keyed on the route so that
       * navigating away from a screen that crashed clears the panel; without
       * the key the boundary would stay latched and the next screen would
       * never render.
       */}
      <ErrorBoundary
        key={boundaryKey}
        title="Something broke while drawing this screen."
      >
        {workbenchOpen ? (
          <Workbench />
        ) : addOpen ? (
          <AddMonitor onCreated={onMonitorCreated} onCancel={closeAdd} />
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
            cardColumns={cardColumns}
            onOpenMonitor={openMonitor}
          />
        )}
      </ErrorBoundary>
    </AppShell>
  );

  return (
    <SessionGate session={session} onSignedIn={onSignedIn} onRetry={refresh}>
      {screen}
    </SessionGate>
  );
}

/** The side track: every component, every state, both themes. */
function Workbench() {
  return (
    <div className="flex flex-col gap-10">
      <p className="text-helper text-ink-3">
        Component workbench — fixtures, not live data. Press Esc to go back to
        the dashboard.
      </p>
      <DashboardWorkbench />
      <HeartbeatGallery />
      <TokenSheet />
    </div>
  );
}
