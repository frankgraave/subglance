import { useState } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { Card, Panel } from "../components/Card";
import { ChangePassword } from "../auth/ChangePassword";
import { SearchIcon, SettingsIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { WatchdogCard } from "../watchdog/Watchdog";
import { RetentionCard } from "../retention/Retention";
import { ResetInstanceCard } from "../reset/ResetInstance";

/** Only implemented sections: no user, token or diagnostics controls yet. The reset card is admin-only. */
export function Settings({ client, canAdmin = false }: { client?: QueryClient; canAdmin?: boolean }) {
  // Like the live pages, Settings owns a provider at its route boundary.
  const [fallback] = useState(createQueryClient);
  const [query, setQuery] = useState("");
  const matches = "account password current new confirm sessions security".includes(query.trim().toLowerCase());
  const watchdogMatches = "self-monitoring watchdog last ping success rejection outage".includes(query.trim().toLowerCase());
  const retentionMatches = "retention storage database history heartbeats summaries incidents disk size".includes(query.trim().toLowerCase());
  // Admin-only, so a search for "reset" by anyone else finds nothing rather
  // than a card they could not use.
  const resetMatches = canAdmin && "reset danger delete all data erase wipe instance".includes(query.trim().toLowerCase());
  return <>
    <TopbarTools>
      <label className="shell-search">
        <SearchIcon />
        <input type="search" className="shell-search-input" aria-label="Search settings" placeholder="Search settings…"
          autoComplete="off" spellCheck={false}
          value={query} onChange={(event) => setQuery(event.target.value)} />
      </label>
    </TopbarTools>
    {/* Filtering hides, rather than unmounts, so a query never discards input. */}
    <div id="account" hidden={!matches} style={{ maxWidth: "var(--size-pane-lg)" }}>
      <Card title="Account" icon={<SettingsIcon />} headingLevel={1}>
        <Panel label="Password"><ChangePassword /></Panel>
      </Card>
    </div>
    <div id="self-monitoring" hidden={!watchdogMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><WatchdogCard /></QueryClientProvider>
    </div>
    <div id="retention" hidden={!retentionMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><RetentionCard canAdmin={canAdmin} /></QueryClientProvider>
    </div>
    {/* Last on the page: the one action here with no undo. */}
    {canAdmin && <div id="reset" hidden={!resetMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><ResetInstanceCard /></QueryClientProvider>
    </div>}
    {!matches && !watchdogMatches && !retentionMatches && !resetMatches && <p role="status">No settings match “{query}”.</p>}
  </>;
}
