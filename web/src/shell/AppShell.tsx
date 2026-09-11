import type { ReactNode } from "react";
import { Sidebar } from "./Sidebar";

/**
 * The frame: sidebar beside content, both scrolling independently.
 *
 * Presentational, like `Dashboard`. It owns no preference and no shortcut —
 * `App` holds those and hands down the two booleans this needs — so the frame
 * can be rendered in a test or a workbench without a storage stub.
 *
 * There is no frame around the status wall: that layout deliberately has no
 * chrome, so the shell renders the wall *instead of* this component.
 */
export type AppShellProps = {
  sidebarCollapsed: boolean;
  instance?: string;
  /** The topbar, rendered sticky above the content column. */
  topbar: ReactNode;
  children: ReactNode;
};

export function AppShell({ sidebarCollapsed, instance, topbar, children }: AppShellProps) {
  return (
    <div className="shell" data-collapsed={sidebarCollapsed ? "true" : "false"}>
      <Sidebar collapsed={sidebarCollapsed} instance={instance} />
      <div className="shell-main">
        {topbar}
        {/*
         * `<main>` starts here, not around the whole grid: the sidebar is
         * navigation, and including it would make "skip to main content" skip
         * to the thing you were trying to skip past.
         */}
        <main className="shell-content">{children}</main>
      </div>
    </div>
  );
}
