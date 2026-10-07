import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { Card, Panel } from "../components/Card";
import { IconAlert, IconDatabase, IconGlobe, IconNetwork, IconServer, IconTransfer } from "../components/icons";
import { ChangePassword } from "../auth/ChangePassword";
import { SettingsIcon } from "../shell/icons";
import { FilterField } from "../shell/FilterField";
import { WatchdogCard } from "../watchdog/Watchdog";
import { BackupCard } from "../backup/Backup";
import { UsersCard } from "../users/Users";
import { TokensCard } from "../tokens/Tokens";
import { DisplayCard, type DisplayPreferences } from "./DisplayCard";

/*
 * The status page editor loads when an administrator opens Settings, not with
 * the app. Nobody else ever sees it, and most administrator sessions never
 * change a page, so shipping it in the entry chunk would make every visitor
 * download two forms and a drawer for someone else's once-a-month task.
 */
const StatusPagesCard = lazy(() => import("../statuspages/StatusPages").then((module) => ({ default: module.StatusPagesCard })));
/*
 * The same for configuration files: an editor's occasional migration task,
 * with a report renderer nobody else needs in the entry chunk.
 */
const ConfigFilesCard = lazy(() => import("../configfile/ConfigFiles").then((module) => ({ default: module.ConfigFilesCard })));
/*
 * And for retention: every role can read it, but nobody reads it on every
 * visit. With the daily pass's record, "Run now" and compaction it is about
 * 6 kB of script, all of it for a card at the bottom of a page most sessions
 * never open, so it follows the two above out of the entry chunk rather than
 * raising the budget for everyone.
 */
const RetentionCard = lazy(() => import("../retention/Retention").then((module) => ({ default: module.RetentionCard })));
/*
 * And for the connectivity check's editor: administrators only, and changed
 * once when an instance is set up if at all. A form, its validation and its
 * conflict handling are not worth carrying in every visitor's entry chunk.
 */
const ConnectivityCard = lazy(() => import("../connectivity/ConnectivityCard").then((module) => ({ default: module.ConnectivityCard })));
/*
 * And for the reset: administrators only, last on the page, and pressed once
 * in an instance's life if at all. It went out of the entry chunk when the
 * not-found screen (SUB-177) took the entry to its ceiling, so a screen every
 * visitor can reach paid for itself with one almost nobody does.
 */
/*
 * And for the instance card: administrators only, near the end of the page,
 * and read when something seems wrong rather than on every visit. It went out
 * of the entry chunk when alert links (SUB-187) gave it a row and took the
 * entry past its ceiling.
 */
const DiagnosticsCard = lazy(() => import("../diagnostics/Diagnostics").then((module) => ({ default: module.DiagnosticsCard })));
const ResetInstanceCard = lazy(() => import("../reset/ResetInstance").then((module) => ({ default: module.ResetInstanceCard })));

/**
 * One settings section: the anchor it answers to, the name the index shows,
 * and the words the settings search matches.
 *
 * The index and the page are drawn from the same list, so a section cannot be
 * on the page without a way to reach it, or in the index without a target.
 */
type Section = { id: string; label: string; keywords: string; body: ReactNode };

/** The fragment of the address, or "" when there is none or it is malformed. */
function fragment(): string {
  try {
    return decodeURIComponent(window.location.hash.slice(1));
  } catch {
    return "";
  }
}

/**
 * Which section the reader is at.
 *
 * While the page scrolls, it is the last visible section whose top has
 * crossed a line a quarter of the way down the viewport: a tall card whose
 * heading has scrolled away is still the card being read. At the bottom of
 * the page it is the last section, which could otherwise never reach the line.
 *
 * A section named by the address (a deep link, an index link, Back) wins
 * until the reader scrolls by hand. The scroll that the jump itself causes
 * must not re-derive the answer: measured in Chromium, `/settings#tokens`
 * scrolled to the end of the page with the retention card still across the
 * line, so the index said "Retention & storage" on the page it had been asked
 * to show the tokens on. Hand input is wheel, touch or key; a programmatic
 * scroll is none of those.
 *
 * The same choice holds the section in place while the cards above it are
 * still loading. Scroll anchoring cannot be relied on for that: when a lazy
 * card above the target resolves in the same frame as the target's own, the
 * node the browser anchored to is replaced, and the target was measured
 * 294px down the page after following its index link (SUB-168). So every
 * change in a section's size while the choice stands scrolls the chosen
 * section back to the top. Hand input of any kind ends it, a press included:
 * a click inside a card that grows it, or on the page's scroll bar, is the
 * reader taking the page back.
 *
 * `ids` are the sections the search leaves visible. When the current section
 * is filtered out, its pin is released and the answer is derived again from
 * what is left, so the index never marks nothing, and clearing the search
 * does not bring back a section the reader had already left.
 */
/** What the reader does with their own hand: a scroll that follows one is theirs. */
const HAND_INPUT = ["wheel", "touchstart", "keydown", "pointerdown"];

function useCurrentSection(ids: string[]): [string | undefined, (id: string) => void] {
  const [current, setCurrent] = useState<string | undefined>(() => {
    const hash = fragment();
    return ids.includes(hash) ? hash : ids[0];
  });
  const pinned = useRef(ids.includes(fragment()));
  const latest = useRef(current);
  useEffect(() => { latest.current = current; });
  const key = ids.join(" ");
  useEffect(() => {
    const visible = () => key.split(" ").map((id) => document.getElementById(id))
      .filter((node): node is HTMLElement => node !== null && !node.hidden);
    const onScroll = () => {
      if (pinned.current) return;
      const nodes = visible();
      if (nodes.length === 0) {
        setCurrent(undefined);
        return;
      }
      const root = document.documentElement;
      if (window.scrollY + window.innerHeight >= root.scrollHeight - 2) {
        setCurrent(nodes[nodes.length - 1].id);
        return;
      }
      const line = window.innerHeight / 4;
      let found = nodes[0].id;
      for (const node of nodes) if (node.getBoundingClientRect().top <= line) found = node.id;
      setCurrent(found);
    };
    const release = () => { pinned.current = false; };
    // Absent in jsdom, which lays nothing out to hold.
    const hold = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => {
      if (pinned.current && latest.current !== undefined) document.getElementById(latest.current)?.scrollIntoView({ block: "start" });
    });
    for (const node of visible()) hold?.observe(node);
    const onHash = () => {
      const hash = fragment();
      if (!key.split(" ").includes(hash)) return;
      pinned.current = true;
      setCurrent(hash);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("hashchange", onHash);
    for (const type of HAND_INPUT) window.addEventListener(type, release, { passive: true });
    if (latest.current === undefined || !key.split(" ").includes(latest.current)) {
      pinned.current = false;
      onScroll();
    }
    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("hashchange", onHash);
      for (const type of HAND_INPUT) window.removeEventListener(type, release);
      hold?.disconnect();
    };
  }, [key]);
  const choose = (id: string) => { pinned.current = true; setCurrent(id); };
  return [current, choose];
}

/**
 * A deep link such as `/settings#tokens` has to land on its section.
 *
 * The browser only scrolls to a fragment that exists when the document loads,
 * and this page is drawn by script after that, so the jump is made here once
 * the sections are mounted. Cards above the target that finish loading later
 * do not pull it away again: `useCurrentSection` holds the section the
 * address chose until the reader takes over.
 */
function useInitialFragment() {
  useEffect(() => {
    const hash = fragment();
    if (hash) document.getElementById(hash)?.scrollIntoView({ block: "start" });
  }, []);
}

/**
 * The settings page: one card per section, in one column, with an index of
 * the sections beside it. `/settings#<section>` is a real address.
 */
export function Settings({ client, canAdmin = false, role = canAdmin ? "admin" : "viewer", userId, display }: {
  client?: QueryClient; canAdmin?: boolean; role?: string; userId?: number;
  /** The browser's display preferences. Without them the section is left out. */
  display?: DisplayPreferences;
}) {
  // Like the live pages, Settings owns a provider at its route boundary.
  const [fallback] = useState(createQueryClient);
  const [query, setQuery] = useState("");
  const provide = (node: ReactNode) => <QueryClientProvider client={client ?? fallback}>{node}</QueryClientProvider>;
  const sections: Section[] = [
    { id: "account", label: "Account", keywords: "account password current new confirm sessions security",
      body: <Card title="Account" icon={<SettingsIcon />} headingLevel={2}>
        <Panel label="Password"><ChangePassword /></Panel>
      </Card> },
    // Second, beside the account: both are about the person at this browser,
    // and every section after them is about the instance.
    ...(display ? [{ id: "display", label: "Display", keywords: "display appearance theme light dark auto system layout rows cards compact wall columns per row browser preferences",
      body: <DisplayCard prefs={display} /> }] : []),
    // Only an administrator can list accounts, so nobody else is shown a card
    // that could only ever say "not allowed".
    ...(canAdmin ? [{ id: "users", label: "Users", keywords: "users accounts roles admin editor viewer people access",
      body: provide(<UsersCard userId={userId} />) }] : []),
    // Admin-only like the API: a page decides what the instance tells people
    // without an account. Beside Users, the other card about who sees what.
    ...(canAdmin ? [{ id: "status-pages", label: "Status pages", keywords: "status pages public page visitors customers share publish slug address",
      body: provide(<Suspense fallback={<Card title="Status pages" icon={<IconGlobe />}><Panel><p>Loading status pages…</p></Panel></Card>}>
        <StatusPagesCard />
      </Suspense>) }] : []),
    { id: "self-monitoring", label: "Self-monitoring", keywords: "self-monitoring watchdog last ping success rejection outage",
      body: provide(<WatchdogCard />) },
    // Admin-only like the endpoint: an address can name a host on the
    // operator's own network. Beside self-monitoring, the other card about
    // how the instance judges its own health.
    ...(canAdmin ? [{ id: "connectivity", label: "Connectivity check",
      keywords: "connectivity check network uplink offline outbound targets addresses gateway host port dns local",
      body: provide(<Suspense fallback={<Card title="Connectivity check" icon={<IconNetwork />}><Panel><p>Loading connectivity settings…</p></Panel></Card>}>
        <ConnectivityCard />
      </Suspense>) }] : []),
    { id: "retention", label: "Retention & storage", keywords: "retention storage database history heartbeats summaries incidents disk size",
      body: provide(<Suspense fallback={<Card title="Retention & storage" icon={<IconDatabase />}><Panel><p>Loading retention settings…</p></Panel></Card>}>
        <RetentionCard canAdmin={canAdmin} />
      </Suspense>) },
    // The endpoint is admin-only (the target names a bucket), so the card is too.
    ...(canAdmin ? [{ id: "backups", label: "Backups", keywords: "backups backup restore s3 bucket storage database snapshot",
      body: provide(<BackupCard />) }] : []),
    // Editors and administrators, like the endpoints: an export stores a key
    // on every object that lacks one, which is a write. Beside backups, the
    // other card about moving an instance's contents, and saying how it differs.
    ...(role === "admin" || role === "editor" ? [{ id: "configuration", label: "Import & export",
      keywords: "configuration files export import yaml download upload dry run migrate move copy instance monitors channels",
      body: provide(<Suspense fallback={<Card title="Import & export" icon={<IconTransfer />}><Panel><p>Loading import and export…</p></Panel></Card>}>
        <ConfigFilesCard />
      </Suspense>) }] : []),
    { id: "tokens", label: "API tokens", keywords: "api tokens bearer keys scripts ci revoke",
      body: provide(<TokensCard role={role} />) },
    // Administrators only: the card names the database path, and the API refuses anyone else.
    ...(canAdmin ? [{ id: "instance", label: "Instance", keywords: "instance diagnostics version build runtime uptime database size wal journal workers pool queue skipped",
      body: provide(<Suspense fallback={<Card title="Instance" icon={<IconServer />}><Panel><p>Loading diagnostics…</p></Panel></Card>}>
        <DiagnosticsCard />
      </Suspense>) }] : []),
    // Last on the page: the one action here with no undo. Admin-only, so a
    // search for "reset" by anyone else finds nothing rather than a card they
    // could not use.
    ...(canAdmin ? [{ id: "reset", label: "Reset instance", keywords: "reset danger delete all data erase wipe instance",
      body: provide(<Suspense fallback={<Card title="Reset this instance" icon={<IconAlert />}><Panel><p>Loading the reset…</p></Panel></Card>}>
        <ResetInstanceCard />
      </Suspense>) }] : []),
  ];
  const needle = query.trim().toLowerCase();
  const shown = sections.filter((section) => section.keywords.includes(needle));
  const [current, setCurrent] = useCurrentSection(shown.map((section) => section.id));
  useInitialFragment();
  return (
    <div className="settings-layout">
      {/*
       * The index is navigation, not state: every link is a real fragment, so
       * the browser scrolls, records history and moves the focus starting
       * point itself. It lists only the sections the search left visible —
       * a link to a hidden section would scroll nowhere.
       * `aria-current="true"`, not "page": the sidebar already says which page
       * this is, and two current pages is a claim neither can keep. The
       * paint is the sidebar's own current-destination rule, reached through
       * the same `data-state`, so "you are here" looks the same at both levels.
       */}
      {/*
       * The filter stands above the index it narrows, in the index's own
       * column (SUB-207): it filters the list of sections, so it belongs at
       * the head of that list rather than in a bar across the page. The two
       * stick together, so the field is still in reach after scrolling
       * halfway down, and the field stays when nothing matches: a filter
       * that vanished with its last result could not be cleared.
       */}
      <div className="settings-aside">
        <FilterField label="Filter settings" placeholder="Filter settings…" value={query} onChange={setQuery} />
        {shown.length > 0 && <nav className="settings-index" aria-label="Settings sections">
          <ul>
            {shown.map((section) => <li key={section.id}>
              <a className="shell-nav-item" href={`#${section.id}`}
                aria-current={current === section.id ? "true" : undefined}
                data-state={current === section.id ? "current" : undefined}
                onClick={() => setCurrent(section.id)}>{section.label}</a>
            </li>)}
          </ul>
        </nav>}
      </div>
      <div className="settings-sections">
        {/* Filtering hides, rather than unmounts, so a query never discards input. */}
        {sections.map((section) => <div key={section.id} id={section.id} className="settings-section"
          hidden={!shown.includes(section)}>{section.body}</div>)}
        {shown.length === 0 && <p role="status">No settings match “{query}”.</p>}
      </div>
    </div>
  );
}
