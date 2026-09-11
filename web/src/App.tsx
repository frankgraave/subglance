import { useCallback, useState } from "react";
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

  const narrow = useCompactViewport();
  const shown = effectiveLayout(layout, narrow);
  const isWall = shown === "wall" && !workbenchOpen;

  const leaveWall = useCallback(() => setLayout("rows"), [setLayout]);
  const toggleWorkbench = useCallback(() => setWorkbenchOpen((open) => !open), []);

  // Esc only means something when there is something to leave. Passing
  // undefined otherwise leaves the key to the browser.
  useShellShortcuts({
    onToggleSidebar: toggleSidebar,
    onEscape: isWall ? leaveWall : workbenchOpen ? toggleWorkbench : undefined,
  });

  if (isWall) {
    return <LiveDashboardRoot layout="wall" onExitWall={leaveWall} />;
  }

  return (
    <AppShell
      sidebarCollapsed={sidebarCollapsed}
      topbar={
        <Topbar
          sidebarCollapsed={sidebarCollapsed}
          onToggleSidebar={toggleSidebar}
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
