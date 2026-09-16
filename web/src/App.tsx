import { useCallback, useEffect, useRef, useState } from "react";
import { SessionGate } from "./auth/SessionGate";
import { useSession } from "./auth/useSession";
import { canWrite } from "./auth/permissions";
import { useTheme } from "./theme/useTheme";
import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";
import { AddMonitor } from "./monitors/AddMonitor";
import { Drawer } from "./components/Drawer";
import { Card, Panel } from "./components/Card";
import { LiveDashboardRoot } from "./live/LiveDashboard";
import { LiveMonitorDetailRoot } from "./live/LiveMonitorDetail";
import { LiveIncidentsRoot } from "./incidents/LiveIncidents";
import { LiveMonitorsRoot } from "./monitors/LiveMonitors";
import { LiveNotificationsRoot } from "./notifications/LiveNotifications";
import { createQueryClient } from "./live/queryClient";
import { monitorsQueryKey } from "./live/api";
import { ErrorBoundary } from "./shell/ErrorBoundary";
import { useRoute } from "./shell/useRoute";
import { routePath } from "./shell/route";
import type { NavRoute } from "./shell/Sidebar";
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
  /*
   * Navigation from the rail and the phone drawer.
   *
   * It closes the two overlays for the same reason `toggleWorkbench` does:
   * the render branch below can only show one thing, and leaving `addOpen`
   * set while navigating would light the add button as pressed over a screen
   * that is not the add form. Same scroll reset as `openMonitor` — a new
   * screen starts at the top.
   */
  const goTo = useCallback(
    (name: NavRoute) => {
      setWorkbenchOpen(false);
      setAddOpen(false);
      navigate(
        name === "monitors" || name === "notifications"
          ? { name, create: false }
          : { name },
      );
      window.scrollTo(0, 0);
    },
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
  const onIncidents = route.name === "incidents";
  const onMonitors = route.name === "monitors";
  const onNotifications = route.name === "notifications";
  /*
   * Opening and closing the create drawer is a navigation, not a boolean.
   *
   * `/monitors/new` is a real address, so closing the drawer has to put the
   * URL back on `/monitors` — otherwise Back would reopen a form the user just
   * dismissed, and a reload would reopen it too.
   */
  const setCreateOpen = useCallback(
    (open: boolean) => navigate({ name: "monitors", create: open }),
    [navigate],
  );
  /* Same argument for the channel form: `/notifications/new` is a real
     address, so dismissing the drawer has to put the URL back. */
  const setChannelCreateOpen = useCallback(
    (open: boolean) => navigate({ name: "notifications", create: open }),
    [navigate],
  );

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
  useDocumentTitle(
    onDetail
      ? "Monitor"
      : onIncidents
        ? "Incidents"
        : onMonitors
          ? "Monitors"
          : onNotifications
            ? "Notifications"
            : "Dashboard",
  );
  const mainRef = useRef<HTMLElement | null>(null);
  useRouteFocus(path, mainRef);

  /*
   * The wall is a dashboard layout, so it cannot be showing while a single
   * monitor is open. Without this, choosing the wall from the detail page
   * would replace it with a chrome-less grid and no way back.
   */
  const isWall =
    shown === "wall" &&
    !workbenchOpen &&
    !onDetail &&
    !onIncidents &&
    !onMonitors &&
    !onNotifications;
  /*
   * What "a different screen" means for the inner error boundary: the route,
   * plus the workbench. Changing either remounts the boundary and so clears a
   * caught error — otherwise a crash on one screen would latch and every
   * screen after it would show the panel instead.
   *
   * The add drawer is deliberately not in the key any more. It is an overlay
   * rather than a screen now, so opening it does not replace what is behind
   * it — and remounting the dashboard every time someone opens the form would
   * throw away its filters and scroll position for a panel that covers a
   * quarter of the width.
   */
  const boundaryKey = `${route.name}:${route.name === "monitor" ? route.id : ""}:${
    workbenchOpen ? "w" : ""
  }`;

  const leaveWall = useCallback(() => setLayout("rows"), [setLayout]);
  /*
   * Leaving the workbench closes the add drawer with it.
   *
   * The two are no longer mutually exclusive as *content* — the drawer is an
   * overlay now, so it can sit over the workbench the way it sits over any
   * other screen — but switching the screen underneath a modal form is a
   * context change, and a form left open across one is a form describing a
   * page that is gone. Closing it here keeps what the add button claims and
   * what is on screen the same thing.
   */
  const toggleWorkbench = useCallback(() => {
    setAddOpen(false);
    setWorkbenchOpen((open) => !open);
  }, []);
  /*
   * Add a monitor, from wherever you pressed it — and it is the same drawer
   * everywhere (SUB-132).
   *
   * It used to be two different things behind one icon: a drawer over the
   * inventory, and on the dashboard a branch that replaced the entire screen
   * with the form. Same glyph, same position, one of them took the monitors
   * away. The drawer is the one that survives, for the reason SUB-122 chose
   * it: you add a monitor *from* the inventory and check it *against* the
   * inventory — that the name is still free, that the interval matches its
   * neighbours — and that argument is no weaker on the dashboard.
   *
   * On the inventory the drawer is the route — `/monitors/new` is a real
   * address — so the topbar has to navigate rather than set a local flag.
   * Setting `addOpen` there would open a second, unrouted copy of the same
   * form over the list, leave the URL on `/monitors`, and light the button as
   * pressed for a state the address bar does not have.
   */
  const toggleAdd = useCallback(() => {
    if (onMonitors) {
      setCreateOpen(!route.create);
      return;
    }
    setAddOpen((open) => !open);
  }, [onMonitors, route, setCreateOpen]);
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
            : onDetail || onIncidents
              ? // Last in the queue, because these are places rather than
                // overlays: anything layered on top of one must be dismissed
                // before Esc means "leave this screen". Both land on the
                // dashboard, which is the one place that always exists.
                //
                // Via `goTo` rather than `showDashboard`, so Esc resets the
                // scroll the same way clicking Dashboard in the rail does.
                // Leaving a long incidents list by keyboard used to arrive at
                // the dashboard scrolled to an offset that belonged to a
                // different screen, with the header off-screen.
                () => goTo("dashboard")
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
      current={route.name}
      onNavigate={goTo}
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
          /*
           * The layout switcher belongs to the dashboard and to nothing else
           * (SUB-131). Rows, Cards, Compact and Status wall are four ways of
           * drawing a monitor list, and Incidents and Monitors do not have
           * one — the control was offering a choice that changed nothing on
           * two of the four screens it appeared on. The workbench is excluded
           * for the same reason: it is a fixture gallery, not a list.
           */
          showLayouts={
            !workbenchOpen && !onDetail && !onIncidents && !onMonitors
          }
          themePreference={preference}
          onThemeChange={setPreference}
          workbenchOpen={workbenchOpen}
          onToggleWorkbench={toggleWorkbench}
          onAddMonitor={toggleAdd}
          addOpen={onMonitors ? route.create : addOpen}
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
        ) : onIncidents ? (
          <LiveIncidentsRoot client={queryClient} />
        ) : route.name === "notifications" ? (
          <LiveNotificationsRoot
            client={queryClient}
            createOpen={route.create}
            onCreateOpenChange={setChannelCreateOpen}
            /* A viewer sees no write controls and no test button: a test is a
               real message to somebody else's inbox, and the server guards it
               with the same write role for that reason. */
            canWrite={session.state === "signedIn" && canWrite(session.user)}
          />
        ) : route.name === "monitors" ? (
          <LiveMonitorsRoot
            client={queryClient}
            onOpen={openMonitor}
            createOpen={route.create}
            onCreateOpenChange={setCreateOpen}
            /* A viewer is shown no write controls at all rather than controls
               that 403: the server's rule is the one consulted, not a guess
               about what this screen would like to offer. */
            canWrite={
              session.state === "signedIn" && canWrite(session.user)
            }
          />
        ) : route.name === "monitor" ? (
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
            onCardColumnsChange={setCardColumns}
            onOpenMonitor={openMonitor}
          />
        )}
      </ErrorBoundary>

      {/*
       * Add a monitor: one drawer, over whatever screen you were on (SUB-132).
       *
       * Rendered here rather than inside each screen because the button that
       * opens it lives in the shell's topbar and is on every screen. The
       * inventory keeps its own copy — there the form is a route,
       * `/monitors/new`, so its open state has to be the URL — and this one
       * is suppressed there so the two cannot both be on screen at once.
       *
       * The form is wrapped in a Card holding a Panel, which is the shape the
       * rest of the product uses and the shape the drawer was built to
       * contain: `AddMonitor` on its own is bare fields on the page
       * background, which is what Frank saw when the dashboard replaced
       * itself with it.
       */}
      {!onMonitors && (
        <Drawer open={addOpen} onClose={closeAdd} title="Add monitor">
          <Card title="New monitor" headingLevel={3}>
            <Panel>
              <AddMonitor onCreated={onMonitorCreated} onCancel={closeAdd} />
            </Panel>
          </Card>
        </Drawer>
      )}
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
