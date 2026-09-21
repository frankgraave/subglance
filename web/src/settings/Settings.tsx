import { useState } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createQueryClient } from "../live/queryClient";
import { Card, Panel } from "../components/Card";
import { ChangePassword } from "../auth/ChangePassword";
import { SearchIcon, SettingsIcon } from "../shell/icons";
import { TopbarTools } from "../shell/TopbarTools";
import { WatchdogCard } from "../watchdog/Watchdog";

/** Only implemented sections: no role, identity, token or retention controls. */
export function Settings({ client }: { client?: QueryClient }) {
  // Like the live pages, Settings owns a provider at its route boundary.
  const [fallback] = useState(createQueryClient);
  const [query, setQuery] = useState("");
  const matches = "account password current new confirm sessions security".includes(query.trim().toLowerCase());
  const watchdogMatches = "self-monitoring watchdog last ping success rejection outage".includes(query.trim().toLowerCase());
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
    {!matches && !watchdogMatches && <p role="status">No settings match “{query}”.</p>}
  </>;
}
