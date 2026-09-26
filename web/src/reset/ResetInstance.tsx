import { useId, useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { Card, Panel } from "../components/Card";
import { IconAlert } from "../components/icons";
import { RESET_PHRASE, resetInstance, type ResetResult } from "./api";

const count = new Intl.NumberFormat("en");

function plural(n: number, one: string, many: string) {
  return `${count.format(n)} ${n === 1 ? one : many}`;
}

function describe(result: ResetResult | null) {
  if (!result) return "The instance was reset.";
  return `Deleted ${plural(result.monitors, "monitor", "monitors")}, ${plural(result.channels, "notification channel", "notification channels")}, ` +
    `${plural(result.maintenance_windows, "maintenance window", "maintenance windows")} and ${plural(result.api_tokens, "API token", "API tokens")}.`;
}

/**
 * Reset this instance: the one action on the settings page with no undo.
 *
 * Outlined, gated and last. The button is a border and a word, never a filled
 * red block: filled red at rest stops being read after the third visit. The
 * typed phrase is the real guard. It is the only interaction on the page that
 * a mis-aimed click cannot complete, and it is compared exactly — the server
 * checks the same string, so the API cannot be emptied by a stray request
 * either.
 *
 * Deliberately absent: "delete my account". On a single-operator instance,
 * deleting the only administrator leaves a running server nobody can sign in
 * to. Accounts, sessions and settings survive a reset for the same reason.
 */
export function ResetInstanceCard() {
  const id = useId();
  const client = useQueryClient();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const matches = typed === RESET_PHRASE;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!matches || busy) return;
    setBusy(true);
    setError(null);
    setDone(null);
    try {
      const result = await resetInstance(typed);
      setTyped("");
      setDone(describe(result));
      // Everything cached about monitors, channels or tokens now describes
      // things that no longer exist.
      void client.invalidateQueries();
    } catch (failure) {
      setError(failure instanceof ApiError
        ? failure.message
        : "Could not reach SubGlance. Nothing was deleted; check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Reset this instance" icon={<IconAlert />}>
      <Panel>
        <form className="auth-form" aria-label="Reset this instance" onSubmit={submit}>
          <p className="retention-note">
            Deletes every monitor, heartbeat, incident, notification channel, maintenance window and API token.
            User accounts, sessions and settings such as retention are kept, so you stay signed in.
            This cannot be undone, and there is no copy unless you made a backup.
          </p>
          <div className="auth-field">
            <label className="auth-label" htmlFor={`${id}-confirm`}>Type <b>{RESET_PHRASE}</b> to enable the button</label>
            <input
              id={`${id}-confirm`} className="auth-input" type="text" value={typed} disabled={busy}
              autoComplete="off" spellCheck={false} autoCapitalize="off"
              aria-describedby={`${id}-state`}
              onChange={(event) => { setTyped(event.target.value); setDone(null); setError(null); }}
            />
            <p className="retention-note" id={`${id}-state`}>
              {matches ? "The phrase matches. Resetting cannot be undone."
                : "The button stays disabled until the phrase matches exactly, including case."}
            </p>
          </div>
          {error && <p className="auth-error" role="alert">{error}</p>}
          <div>
            <button className="add-button" type="submit" disabled={!matches || busy}>
              {busy ? "Deleting…" : "Delete all data"}
            </button>
          </div>
          {done && <p role="status">{done}</p>}
        </form>
      </Panel>
    </Card>
  );
}
