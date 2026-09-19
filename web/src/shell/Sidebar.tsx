import {
  DASHBOARD_PATH,
  INCIDENTS_PATH,
  MONITORS_PATH,
  NOTIFICATIONS_PATH,
  SETTINGS_PATH,
} from "./route";
import {
  DashboardIcon,
  IncidentsIcon,
  MonitorsIcon,
  NotificationsIcon,
  SettingsIcon,
  SignOutIcon,
} from "./icons";

/**
 * The primary navigation.
 *
 * Every destination is a real screen. Settings opens the caller's password
 * card; unfinished settings sections are not advertised as usable controls.
 * Counts belong on their screens, next to the lists they describe.
 *
 * **Incidents is a real destination now (SUB-34).** It was the oldest of the
 * "Soon" promises and the one the product could least afford to keep breaking:
 * `GET /api/v1/incidents` has been answering "what is broken right now" since
 * the backend landed, and until this release the only way to ask was to open
 * monitors one at a time. It carries no count — the count belongs on the
 * screen, next to the list it is the length of, where the two cannot disagree.
 *
 * **Monitors is a real destination now (SUB-122).** It was the most misleading
 * "Soon" left: the dashboard is full of monitors, so an item called Monitors
 * that leads nowhere reads as a broken link to the screen already on display.
 * It now leads to the inventory, which is a different question — not "is
 * anything wrong" but "what is configured, and how".
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

type BuiltDestination = Destination & {
  href: string;
  /** Which route name lights this item up. */
  route: NavRoute;
};

const BUILT: readonly BuiltDestination[] = [
  {
    id: "dashboard",
    label: "Dashboard",
    Icon: DashboardIcon,
    href: DASHBOARD_PATH,
    route: "dashboard",
  },
  {
    id: "incidents",
    label: "Incidents",
    Icon: IncidentsIcon,
    href: INCIDENTS_PATH,
    route: "incidents",
  },
  /*
   * **Monitors is a real destination now (SUB-122).** It was the last of the
   * "Soon" promises in this group and the most misleading one: the dashboard
   * shows monitors, so an item labelled Monitors that goes nowhere reads as a
   * broken link to the screen you are already looking at. It leads to the
   * inventory — the page that shows what the dashboard refuses to (interval,
   * timeout, retries, channels, tags) and is where those are changed.
   */
  {
    id: "monitors",
    label: "Monitors",
    Icon: MonitorsIcon,
    href: MONITORS_PATH,
    route: "monitors",
  },
];

/**
 * The Configure group's real destinations.
 *
 * **Notifications is a real destination now (SUB-123).** It was the "Soon" with
 * the worst consequence attached: the channel endpoints have existed since the
 * backend landed, so an instance could be running with no channel at all —
 * every alert going nowhere — and the only item in the navigation that would
 * have said so was inert. It leads to the channel list, which is where that is
 * discovered and fixed.
 */
const BUILT_CONFIG: readonly BuiltDestination[] = [
  {
    id: "notifications",
    label: "Notifications",
    Icon: NotificationsIcon,
    href: NOTIFICATIONS_PATH,
    route: "notifications",
  },
  { id: "settings", label: "Settings", Icon: SettingsIcon, href: SETTINGS_PATH, route: "settings" },
];

/** The destinations the rail can navigate to. */
export type NavRoute =
  | "dashboard"
  | "incidents"
  | "monitors"
  | "notifications"
  | "settings";

export type SidebarProps = {
  collapsed: boolean;
  /** Shown under the product name; the instance this dashboard watches. */
  instance?: string;
  /** Which built destination is on screen. Defaults to the dashboard. */
  current?: NavRoute | "monitor";
  /** Client-side navigation. Absent means the links do a full page load. */
  onNavigate?: (route: NavRoute) => void;
  /** The signed-in address. Absent means no account footer is drawn. */
  account?: string;
  onSignOut?: () => void;
};

/**
 * A destination that exists: a real `<a href>`, for the reasons `MonitorLink`
 * spells out. Middle-click opens it in a tab, right-click copies the address,
 * and the status bar shows where it goes — none of which a click handler on a
 * `<span>` gets. The handler on top is an optimisation that stands aside the
 * moment the user asks for anything but a plain left click.
 */
function Built({
  item,
  current,
  onNavigate,
}: {
  item: BuiltDestination;
  current: boolean;
  onNavigate?: (route: NavRoute) => void;
}) {
  return (
    <li>
      <a
        className="shell-nav-item"
        href={item.href}
        data-state={current ? "current" : "available"}
        /*
         * aria-current="page" rather than a styled class alone: which screen
         * you are on should reach a screen reader without it reading the
         * stylesheet.
         */
        {...(current ? { "aria-current": "page" as const } : {})}
        onClick={(event) => {
          if (onNavigate === undefined) return;
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
          onNavigate(item.route);
        }}
      >
        <item.Icon />
        <span className="shell-nav-text">{item.label}</span>
      </a>
    </li>
  );
}

export function Sidebar({
  collapsed,
  instance,
  current = "dashboard",
  onNavigate,
  account,
  onSignOut,
}: SidebarProps) {
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
        {BUILT.map((item) => (
          <Built
            key={item.id}
            item={item}
            /*
             * A monitor's detail view belongs to the dashboard branch of the
             * product, so Dashboard stays lit while you are on one. Lighting
             * nothing would leave the rail claiming you are nowhere.
             */
            current={
              current === item.route ||
              (current === "monitor" && item.route === "dashboard")
            }
            onNavigate={onNavigate}
          />
        ))}
      </ul>

      <p className="shell-nav-label">Configure</p>
      <ul className="shell-nav">
        {BUILT_CONFIG.map((item) => (
          <Built
            key={item.id}
            item={item}
            current={current === item.route}
            onNavigate={onNavigate}
          />
        ))}
      </ul>

      {/*
       * Who you are, and the way out, at the bottom of the navigation.
       *
       * This is where an account menu is looked for, and it is the only
       * chrome that persists on every screen — putting sign-out in the
       * topbar would spend one of the few slots a phone has on an action
       * taken once a session. The address is shown because a self-hoster
       * with an admin and a viewer account has no other way to tell which
       * one this browser is holding.
       */}
      {account !== undefined && account !== "" && onSignOut !== undefined && (
        <div className="shell-account">
          <p className="shell-account-email" title={account}>
            {account}
          </p>
          <button type="button" className="shell-signout" onClick={onSignOut}>
            {/* The icon is what remains visible on the collapsed rail; the
                label survives it too, clipped rather than dropped, so the
                button is never an unnamed icon to a screen reader. */}
            <SignOutIcon />
            <span className="shell-nav-text">Sign out</span>
          </button>
        </div>
      )}
    </nav>
  );
}
