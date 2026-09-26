import { useState } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { Card, Panel } from "../components/Card";
import { ChangePassword } from "../auth/ChangePassword";
import { SearchIcon, SettingsIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { WatchdogCard } from "../watchdog/Watchdog";
import { RetentionCard } from "../retention/Retention";
import { UsersCard } from "../users/Users";

/** Only implemented sections: no token or diagnostics controls yet. */
export function Settings({ client, canAdmin = false, userId }: { client?: QueryClient; canAdmin?: boolean; userId?: number }) {
  // Like the live pages, Settings owns a provider at its route boundary.
  const [fallback] = useState(createQueryClient);
  const [query, setQuery] = useState("");
  const matches = "account password current new confirm sessions security".includes(query.trim().toLowerCase());
  const watchdogMatches = "self-monitoring watchdog last ping success rejection outage".includes(query.trim().toLowerCase());
  const retentionMatches = "retention storage database history heartbeats summaries incidents disk size".includes(query.trim().toLowerCase());
  // Only an administrator can list accounts, so nobody else is shown a card
  // that could only ever say "not allowed".
  const usersMatches = canAdmin && "users accounts roles admin editor viewer people access".includes(query.trim().toLowerCase());
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
    {canAdmin && <div id="users" hidden={!usersMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><UsersCard userId={userId} /></QueryClientProvider>
    </div>}
    <div id="self-monitoring" hidden={!watchdogMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><WatchdogCard /></QueryClientProvider>
    </div>
    <div id="retention" hidden={!retentionMatches} style={{ maxWidth: "var(--size-pane-lg)", marginTop: "var(--space-4)" }}>
      <QueryClientProvider client={client ?? fallback}><RetentionCard canAdmin={canAdmin} /></QueryClientProvider>
    </div>
    {!matches && !watchdogMatches && !retentionMatches && !usersMatches && <p role="status">No settings match “{query}”.</p>}
  </>;
}
