import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { StateChip } from "../components/Chip";
import { ApiError } from "../api/http";
import { createToken, fetchTokens, revokeToken, rolesWithin, tokensKey, type ApiToken, type TokenRole } from "./api";

const EXPIRY: [string, string][] = [["", "Never"], ["720h", "30 days"], ["2160h", "90 days"], ["8760h", "1 year"]];
const ROLE_HELP: Record<TokenRole, string> = {
  viewer: "Viewer: reads monitors, incidents and metrics. Right for a dashboard or a badge.",
  editor: "Editor: also creates and changes monitors and acknowledges incidents. Right for a deploy pipeline.",
  admin: "Admin: also manages accounts. Give a script this only when it has to.",
};

const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });

/**
 * The secret, shown once.
 *
 * It lives in its own panel that only a create can produce and only an
 * explicit dismissal removes: it does not fade, and it is component state, so
 * a reload loses it rather than fetching it again. The text stays selectable
 * because `navigator.clipboard` is missing on a plain-HTTP LAN address, which
 * is how a self-hosted instance is often first reached.
 */
function Reveal({ token, name, onDone }: { token: string; name: string; onDone: () => void }) {
  const [copied, setCopied] = useState<string | null>(null);
  const id = useId();
  const panel = useRef<HTMLDivElement>(null);
  // The create button is still there, but the next thing to read is this.
  useEffect(() => { panel.current?.focus(); }, []);
  const copy = () => {
    if (!navigator.clipboard) { setCopied("Select the token and copy it by hand: this browser blocks the clipboard here."); return; }
    navigator.clipboard.writeText(token).then(() => setCopied("Copied."), () => setCopied("Could not copy. Select the token and copy it by hand."));
  };
  return (
    <div ref={panel} tabIndex={-1} className="push-reveal" aria-labelledby={`${id}-title`}>
      <p className="auth-label" id={`${id}-title`}>New token for {name}, shown once</p>
      <p role="alert" className="push-reveal-warn">
        This is the only time this token is shown. SubGlance stores a hash of it, so if it is lost, revoke it and create another.
      </p>
      <div className="push-reveal-row">
        <input className="add-input push-reveal-input" aria-label={`Token for ${name}`} value={token} readOnly spellCheck={false}
          onFocus={(event) => event.currentTarget.select()} />
        <button type="button" className="add-button" onClick={copy}>Copy</button>
      </div>
      <div role="status" aria-live="polite" className="add-result-region">{copied && <p className="add-result">{copied}</p>}</div>
      <div><button type="button" className="add-button add-button-primary" onClick={onDone}>I have saved it</button></div>
    </div>
  );
}

function CreateForm({ role, onCreated }: { role: string; onCreated: (token: string, details: ApiToken) => void }) {
  const id = useId();
  const roles = rolesWithin(role);
  const [name, setName] = useState("");
  // Least privilege by default: the reader has to choose to give more.
  const [scope, setScope] = useState<TokenRole>("viewer");
  const [expiry, setExpiry] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<{ field?: string; message: string } | null>(null);
  // A role refusal is drawn under the role select, not with the general errors.
  const roleError = error?.field === "role";

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!name.trim() || saving) return;
    setSaving(true);
    setError(null);
    try {
      const created = await createToken({ name: name.trim(), role: scope, ...(expiry ? { expires_in: expiry } : {}) });
      setName("");
      onCreated(created.token, created.details);
    } catch (err) {
      setError(err instanceof ApiError ? { field: err.field ?? undefined, message: err.message }
        : { message: err instanceof Error ? err.message : "Could not reach SubGlance. Check your connection and try again." });
    } finally {
      setSaving(false);
    }
  }

  return (
    <form className="auth-form" aria-label="Create API token" onSubmit={submit}>
      <div className="retention-inputs">
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-name`}>Token name</label>
          <input className="auth-input" id={`${id}-name`} value={name} maxLength={100} autoComplete="off" spellCheck={false}
            placeholder="grafana" aria-describedby={error?.field === "name" ? `${id}-error` : undefined}
            aria-invalid={error?.field === "name" ? true : undefined} onChange={(event) => setName(event.target.value)} />
        </div>
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-role`}>Role</label>
          <select className="auth-input" id={`${id}-role`} value={scope}
            aria-describedby={roleError ? `${id}-role-help ${id}-error` : `${id}-role-help`}
            aria-invalid={roleError ? true : undefined}
            onChange={(event) => setScope(event.target.value as TokenRole)}>
            {roles.map((item) => <option key={item} value={item}>{item[0].toUpperCase() + item.slice(1)}</option>)}
          </select>
          {roleError && <p className="auth-error" role="alert" id={`${id}-error`}>{error.message}</p>}
        </div>
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-expiry`}>Expires</label>
          <select className="auth-input" id={`${id}-expiry`} value={expiry} onChange={(event) => setExpiry(event.target.value)}>
            {EXPIRY.map(([value, label]) => <option key={label} value={value}>{label}</option>)}
          </select>
        </div>
      </div>
      <p className="retention-note" id={`${id}-role-help`}>{ROLE_HELP[scope]}</p>
      {error && !roleError && <p className="auth-error" role="alert" id={`${id}-error`}>{error.message}</p>}
      <div><button className="auth-submit" type="submit" disabled={!name.trim() || saving}>{saving ? "Creating…" : "Create token"}</button></div>
    </form>
  );
}

/** Neither revoked nor past its expiry, measured at `now`. */
function isLive(token: ApiToken, now: number): boolean {
  return !token.revoked_at && !(token.expires_at !== undefined && Date.parse(token.expires_at) <= now);
}

function TokenRow({ token, now, onRevoked }: { token: ApiToken; now: number; onRevoked: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const expired = token.expires_at !== undefined && Date.parse(token.expires_at) <= now;
  const live = isLive(token, now);
  const facts = [
    `created ${day(token.created_at)}`,
    token.last_used_at ? `last used ${day(token.last_used_at)}` : "never used",
    token.revoked_at ? `revoked ${day(token.revoked_at)}` : token.expires_at ? `${expired ? "expired" : "expires"} ${day(token.expires_at)}` : "no expiry",
  ];

  async function revoke() {
    setBusy(true);
    setError(null);
    try {
      await revokeToken(token.id);
      onRevoked();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not revoke the token.");
      setBusy(false);
    }
  }

  // Revoke is a neutral button: --down text on a dark panel measures 3.9:1,
  // under the 4.5:1 a label needs. The second step is what guards it.
  return (
    <li className="retention-inputs">
      <div className="tokens-main">
        <p><strong>{token.name}</strong> <span className="nt-mask">{token.prefix}…</span></p>
        <p className="retention-note">{facts.join(" · ")}</p>
        {error && <p className="auth-error" role="alert">{error}</p>}
      </div>
      <StateChip>{live ? token.role : token.revoked_at ? "revoked" : "expired"}</StateChip>
      {live && (confirming ? (
        <span className="add-actions">
          <button type="button" className="add-button" disabled={busy} onClick={() => void revoke()}
            aria-label={`Confirm revoking ${token.name}`}>Revoke now</button>
          <button type="button" className="add-button" disabled={busy} onClick={() => setConfirming(false)}>Keep</button>
        </span>
      ) : (
        <button type="button" className="add-button" aria-label={`Revoke ${token.name}`} onClick={() => setConfirming(true)}>Revoke</button>
      ))}
    </li>
  );
}

/** The caller's own API tokens: create one with a role, copy it once, revoke it. */
export function TokensCard({ role }: { role: string }) {
  const client = useQueryClient();
  const query = useQuery({ queryKey: tokensKey, queryFn: ({ signal }) => fetchTokens(signal) });
  const [secret, setSecret] = useState<{ token: string; name: string } | null>(null);
  const canCreate = role === "admin" || role === "editor";
  const refresh = () => void client.invalidateQueries({ queryKey: tokensKey });
  const tokens = query.data ?? [];
  // Read when the list arrives, not on every render, so a re-render cannot
  // move a token from live to expired between two parts of the same card.
  const now = query.dataUpdatedAt;
  // Live tokens first: the list is where a leaked one gets found and revoked.
  const ordered = [...tokens].sort((a, b) => Number(!isLive(a, now)) - Number(!isLive(b, now)));
  const live = tokens.filter((t) => isLive(t, now)).length;

  return (
    <Card title="API tokens" className="retention-card" note={query.data ? `${live} active` : undefined}>
      <Panel>
        <p className="retention-note">
          For scripts and CI, sent as <code>Authorization: Bearer</code>. A token acts with its own role, never more than your account&apos;s.
        </p>
        {secret && <Reveal token={secret.token} name={secret.name} onDone={() => setSecret(null)} />}
        {canCreate
          ? !secret && <CreateForm role={role} onCreated={(token, details) => { setSecret({ token, name: details.name }); refresh(); }} />
          : <p className="retention-note">A viewer can list and revoke its own tokens but not create one.</p>}
        {!query.data ? <p>{query.isError ? "API tokens unavailable." : "Loading API tokens…"}</p>
          : ordered.length === 0 ? <p className="retention-note">No tokens yet.</p>
          : <ul className="auth-form" aria-label="Your API tokens">
            {ordered.map((token) => <TokenRow key={token.id} token={token} now={now} onRevoked={refresh} />)}
          </ul>}
      </Panel>
    </Card>
  );
}
