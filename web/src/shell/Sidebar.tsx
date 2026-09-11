import {
  DashboardIcon,
  IncidentsIcon,
  MonitorsIcon,
  NotificationsIcon,
  SettingsIcon,
} from "./icons";

/**
 * The primary navigation.
 *
 * **It does not promise what does not exist.** The mockup's sidebar advertises
 * five destinations and one of them is built (DESIGN.md §12); its "2
 * incidents" badge is fixture data. A badge that claims two open incidents and
 * goes nowhere is worse than no badge at all — on a monitoring tool it is
 * indistinguishable from a real alert. So the four unbuilt destinations are
 * rendered as plainly unavailable: dimmed, not focusable as actions, each
 * saying "Soon" in words rather than relying on colour.
 *
 * They are shown rather than hidden because the shape of the product is
 * information too, and because a sidebar that grows items one release at a
 * time keeps moving the one item that works.
 *
 * **Collapsed it becomes a rail, never nothing.** Hiding navigation entirely
 * leaves no way back to it; a 56px icon rail keeps every destination one click
 * away. The labels stay in the DOM when collapsed, hidden the accessible way,
 * so a screen reader still reads "Dashboard" rather than an unnamed button.
 */

type Destination = {
  id: string;
  label: string;
  Icon: (props: { className?: string }) => React.ReactElement;
};

const AVAILABLE: Destination = { id: "dashboard", label: "Dashboard", Icon: DashboardIcon };

const PLANNED: readonly Destination[] = [
  { id: "incidents", label: "Incidents", Icon: IncidentsIcon },
  { id: "monitors", label: "Monitors", Icon: MonitorsIcon },
];

const PLANNED_CONFIG: readonly Destination[] = [
  { id: "notifications", label: "Notifications", Icon: NotificationsIcon },
  { id: "settings", label: "Settings", Icon: SettingsIcon },
];

export type SidebarProps = {
  collapsed: boolean;
  /** Shown under the product name; the instance this dashboard watches. */
  instance?: string;
};

function Planned({ label, Icon }: Destination) {
  return (
    <li>
      {/*
       * A <span>, not a disabled <button>. There is nothing to press, and a
       * disabled button in a nav list is a promise with the wiring cut. The
       * "Soon" text is read out, so the state does not depend on the dimming.
       */}
      <span className="shell-nav-item" data-state="planned" title={`${label} — not built yet`}>
        <Icon />
        <span className="shell-nav-text">{label}</span>
        <span className="shell-nav-soon">Soon</span>
      </span>
    </li>
  );
}

export function Sidebar({ collapsed, instance }: SidebarProps) {
  return (
    <nav
      className="shell-sidebar"
      data-collapsed={collapsed ? "true" : "false"}
      aria-label="Primary"
    >
      <div className="shell-brand">
        <span className="shell-brand-mark" aria-hidden="true">
          <span className="led" data-state="up" />
        </span>
        <span className="shell-brand-copy">
          <span className="shell-brand-name">SubGlance</span>
          {instance !== undefined && instance !== "" && (
            <span className="shell-brand-sub">{instance}</span>
          )}
        </span>
      </div>

      <ul className="shell-nav">
        <li>
          {/*
           * aria-current="page" rather than a styled class alone: the single
           * built destination is also the one you are always on, and that fact
           * should reach a screen reader without reading the stylesheet.
           */}
          <span className="shell-nav-item" data-state="current" aria-current="page">
            <AVAILABLE.Icon />
            <span className="shell-nav-text">{AVAILABLE.label}</span>
          </span>
        </li>
        {PLANNED.map((item) => (
          <Planned key={item.id} {...item} />
        ))}
      </ul>

      <p className="shell-nav-label">Configure</p>
      <ul className="shell-nav">
        {PLANNED_CONFIG.map((item) => (
          <Planned key={item.id} {...item} />
        ))}
      </ul>
    </nav>
  );
}
