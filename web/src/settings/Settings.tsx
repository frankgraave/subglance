import { useEffect, useRef, useState, type ReactNode } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { Card, Panel } from "../components/Card";
import { ChangePassword } from "../auth/ChangePassword";
import { SearchIcon, SettingsIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { WatchdogCard } from "../watchdog/Watchdog";
import { RetentionCard } from "../retention/Retention";
import { TokensCard } from "../tokens/Tokens";

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
 * `ids` are the sections the search leaves visible. When the current section
 * is filtered out, its pin is released and the answer is derived again from
 * what is left, so the index never marks nothing, and clearing the search
 * does not bring back a section the reader had already left.
 */
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
    const onHash = () => {
      const hash = fragment();
      if (!key.split(" ").includes(hash)) return;
      pinned.current = true;
      setCurrent(hash);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("hashchange", onHash);
    for (const type of ["wheel", "touchstart", "keydown"]) window.addEventListener(type, release, { passive: true });
    if (latest.current === undefined || !key.split(" ").includes(latest.current)) {
      pinned.current = false;
      onScroll();
    }
    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("hashchange", onHash);
      for (const type of ["wheel", "touchstart", "keydown"]) window.removeEventListener(type, release);
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
 * do not pull it away again: scroll anchoring keeps the viewport on the
 * element it was showing.
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
export function Settings({ client, canAdmin = false, role = canAdmin ? "admin" : "viewer" }: { client?: QueryClient; canAdmin?: boolean; role?: string }) {
  // Like the live pages, Settings owns a provider at its route boundary.
  const [fallback] = useState(createQueryClient);
  const [query, setQuery] = useState("");
  const provide = (node: ReactNode) => <QueryClientProvider client={client ?? fallback}>{node}</QueryClientProvider>;
  const sections: Section[] = [
    { id: "account", label: "Account", keywords: "account password current new confirm sessions security",
      body: <Card title="Account" icon={<SettingsIcon />} headingLevel={1}>
        <Panel label="Password"><ChangePassword /></Panel>
      </Card> },
    { id: "self-monitoring", label: "Self-monitoring", keywords: "self-monitoring watchdog last ping success rejection outage",
      body: provide(<WatchdogCard />) },
    { id: "retention", label: "Retention & storage", keywords: "retention storage database history heartbeats summaries incidents disk size",
      body: provide(<RetentionCard canAdmin={canAdmin} />) },
    { id: "tokens", label: "API tokens", keywords: "api tokens bearer keys scripts ci revoke",
      body: provide(<TokensCard role={role} />) },
  ];
  const needle = query.trim().toLowerCase();
  const shown = sections.filter((section) => section.keywords.includes(needle));
  const [current, setCurrent] = useCurrentSection(shown.map((section) => section.id));
  useInitialFragment();
  return <>
    <TopbarTools>
      <label className="shell-search">
        <SearchIcon />
        <input type="search" className="shell-search-input" aria-label="Search settings" placeholder="Search settings…"
          autoComplete="off" spellCheck={false}
          value={query} onChange={(event) => setQuery(event.target.value)} />
      </label>
    </TopbarTools>
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
      <div className="settings-sections">
        {/* Filtering hides, rather than unmounts, so a query never discards input. */}
        {sections.map((section) => <div key={section.id} id={section.id} className="settings-section"
          hidden={!shown.includes(section)}>{section.body}</div>)}
        {shown.length === 0 && <p role="status">No settings match “{query}”.</p>}
      </div>
    </div>
  </>;
}
