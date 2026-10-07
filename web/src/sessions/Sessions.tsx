import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { StateChip } from "../components/Chip";
import { FieldError } from "../components/FieldError";
import { formatDateIso, formatMomentIso } from "../format/format";
import {
  endOtherSessions, endSession, endUserSessions, fetchSessions, fetchUserSessions,
  sessionsKey, userSessionsKey, type BrowserSession,
} from "./api";
import { sessionName } from "./sessionName";

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

const failure = (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback;

/**
 * One session: what it is, where it came from, when it was last used.
 *
 * The current session says so instead of carrying a "Sign out": the sidebar's
 * own Sign out ends it, and a button here would drop the reader on the sign-in
 * screen from the middle of their settings.
 */
function SessionRow({ session, action, error }: { session: BrowserSession; action?: ReactNode; error?: string | null }) {
  const name = sessionName(session);
  const recognised = Boolean(session.browser && session.platform);
  const facts = [
    session.ip || "address unknown",
    `signed in ${formatDateIso(session.created_at)}`,
    session.current ? "in use now" : `last seen ${formatMomentIso(session.last_seen_at)}`,
  ];
  return (
    <li className="control-row">
      <div className="control-row-main">
        <p><strong>{name}</strong></p>
        <p className="panel-note">{facts.join(" · ")}</p>
        {!recognised && session.user_agent && <p className="panel-note"><code>{session.user_agent}</code></p>}
        {error && <FieldError>{error}</FieldError>}
      </div>
      {session.current ? <StateChip>this browser</StateChip> : action}
    </li>
  );
}

function OwnSessionRow({ session, onEnded }: { session: BrowserSession; onEnded: (message: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const name = sessionName(session);
  // Named with the address and the last use as well: two Firefox sessions on
  // two Macs are two buttons a screen reader must be able to tell apart.
  const label = `Sign out ${name} at ${session.ip || "an unknown address"}, last seen ${formatMomentIso(session.last_seen_at)}`;

  async function end() {
    setBusy(true);
    setError(null);
    try {
      await endSession(session.id);
      onEnded(`Signed out ${name}.`);
    } catch (err) {
      setError(failure(err, "Could not sign that session out."));
      setBusy(false);
    }
  }

  return (
    <SessionRow session={session} error={error} action={
      // One press, no confirmation: nothing is lost, and the person on the
      // other end signs in again. A confirmation is for what cannot be undone.
      <button type="button" className="button button--compact" disabled={busy} aria-label={label}
        onClick={() => void end()}>Sign out</button>
    } />
  );
}

/**
 * The signed-in account's own sessions, under Settings → Account: sign one
 * out, or every one but this browser.
 *
 * Changing the password also signs every other session out; this is the way
 * to end one lost laptop's session without doing that.
 */
export function SessionsPanel() {
  const client = useQueryClient();
  const query = useQuery({ queryKey: sessionsKey, queryFn: ({ signal }) => fetchSessions(signal) });
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const sessions = query.data;
  const others = sessions?.filter((session) => !session.current).length ?? 0;
  const done = (text: string) => {
    setMessage(text);
    void client.invalidateQueries({ queryKey: sessionsKey });
  };

  async function endOthers() {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const ended = await endOtherSessions();
      done(ended === 0 ? "There were no other sessions to sign out." : `Signed out ${plural(ended, "other session", "other sessions")}.`);
    } catch (err) {
      setError(failure(err, "Could not sign the other sessions out."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <p className="panel-note">
        Every browser signed in to this account. Signing one out ends it on its next request; API tokens are separate.
        Last seen moves at most once an hour.
      </p>
      {!sessions ? <p>{query.isError ? "Sessions unavailable." : "Loading sessions…"}</p>
        : <ul className="stack" aria-label="Your sessions">
          {sessions.map((session) => session.current
            ? <SessionRow key={session.id} session={session} />
            : <OwnSessionRow key={session.id} session={session} onEnded={done} />)}
        </ul>}
      <div role="status" aria-live="polite" className="result-region">{message && <p className="result">{message}</p>}</div>
      {error && <FieldError>{error}</FieldError>}
      <div className="button-row">
        <button type="button" className="button" disabled={busy || others === 0} onClick={() => void endOthers()}>
          Sign out everywhere else
        </button>
      </div>
    </div>
  );
}

/**
 * Another account's sessions, for an administrator under Users: where it is
 * signed in, and one button that signs it out everywhere.
 *
 * There is no per-session button here. An administrator is answering "this
 * person lost a device", and ending every session answers it without having
 * to guess which row the device was; the account keeps its password, role
 * and API tokens and signs in again.
 */
export function AccountSessions({ userId, email }: { userId: number; email: string }) {
  const client = useQueryClient();
  const key = userSessionsKey(userId);
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => fetchUserSessions(userId, signal) });
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const sessions = query.data;

  async function endAll() {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const ended = await endUserSessions(userId);
      setMessage(`${email} is signed out of ${plural(ended, "session", "sessions")}.`);
      void client.invalidateQueries({ queryKey: key });
    } catch (err) {
      setError(failure(err, "Could not sign the sessions out."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      {!sessions ? <p>{query.isError ? "Sessions unavailable." : "Loading sessions…"}</p>
        : sessions.length === 0 ? <p className="panel-note">Not signed in anywhere.</p>
        : <ul className="stack" aria-label={`Sessions of ${email}`}>
          {sessions.map((session) => <SessionRow key={session.id} session={session} />)}
        </ul>}
      <div role="status" aria-live="polite" className="result-region">{message && <p className="result">{message}</p>}</div>
      {error && <FieldError>{error}</FieldError>}
      <div className="button-row">
        {/* Named with the address, which the visible words lead: the card
            lists every account, so "Sign out everywhere" alone is ambiguous
            to a screen reader moving between buttons. */}
        <button type="button" className="button" disabled={busy || !sessions || sessions.length === 0}
          aria-label={`Sign out everywhere for ${email}`} onClick={() => void endAll()}>Sign out everywhere</button>
      </div>
    </div>
  );
}
